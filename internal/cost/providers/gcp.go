// File: internal/cost/providers/gcp.go
package providers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	billingcatalogmanagement "google.golang.org/api/billingbudgets/v1"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/option"
)

// GCPCostProvider implements cost retrieval for GCP resources
type GCPCostProvider struct {
	compute       *compute.InstancesClient
	billing       *cloudbilling.Service
	catalogSvc    *billingcatalogmanagement.Service
	projectID     string
	zone          string
	cache         map[string]float64
	cacheExpiry   time.Time
	cacheDuration time.Duration
}

// GCPResourceCost represents the cost structure for GCP resources
type GCPResourceCost struct {
	ResourceID   string
	ResourceType string
	MachineType  string
	HourlyRate   float64
	MonthlyRate  float64
	Zone         string
	Region       string
	Labels       map[string]string
}

// NewGCPCostProvider creates a new GCP cost provider
func NewGCPCostProvider(endpoint string) *GCPCostProvider {
	ctx := context.Background()

	// Create compute client
	computeClient, err := compute.NewInstancesRESTClient(ctx, option.WithEndpoint(endpoint))
	if err != nil {
		log.Fatalf("Failed to create compute client: %v", err)
	}

	// Create billing client
	billingService, err := cloudbilling.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to create billing client: %v", err)
	}

	// Create billing catalog service
	catalogService, err := billingcatalogmanagement.NewService(ctx)
	if err != nil {
		log.Printf("Warning: Failed to create billing catalog service: %v", err)
	}

	// Get project ID from metadata or environment
	projectID := getGCPProjectID()

	// Default zone if not specified
	zone := "us-central1-a"

	return &GCPCostProvider{
		compute:       computeClient,
		billing:       billingService,
		catalogSvc:    catalogService,
		projectID:     projectID,
		zone:          zone,
		cache:         make(map[string]float64),
		cacheExpiry:   time.Now(),
		cacheDuration: 24 * time.Hour,
	}
}

// GetResourceCosts retrieves costs for resources in the cluster
func (p *GCPCostProvider) GetResourceCosts(resources map[string]interface{}) (map[string]float64, error) {
	costs := make(map[string]float64)

	// Extract nodes from resources
	nodes, ok := resources["nodes"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to extract nodes from resources")
	}

	// Get instance details for each node
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		nodeName, ok := nodeMap["metadata"].(map[string]interface{})["name"].(string)
		if !ok {
			continue
		}

		// Extract instance details from node metadata
		instanceName, zone := p.extractInstanceInfo(nodeMap)
		if instanceName == "" {
			continue
		}

		// Get instance details and cost
		cost, err := p.getInstanceCost(instanceName, zone)
		if err != nil {
			log.Printf("Failed to get cost for instance %s: %v", instanceName, err)
			continue
		}

		costs[nodeName] = cost
	}

	return costs, nil
}

// extractInstanceInfo extracts GCP instance name and zone from node metadata
func (p *GCPCostProvider) extractInstanceInfo(nodeMap map[string]interface{}) (string, string) {
	// Try to get provider ID first
	if status, ok := nodeMap["status"].(map[string]interface{}); ok {
		if nodeInfo, ok := status["nodeInfo"].(map[string]interface{}); ok {
			if providerID, ok := nodeInfo["machineID"].(string); ok {
				// Provider ID format: gce://project/zone/instance-name
				parts := strings.Split(providerID, "/")
				if len(parts) >= 5 {
					zone := parts[3]
					instanceName := parts[4]
					return instanceName, zone
				}
			}
		}
	}

	// Try to get from annotations
	if metadata, ok := nodeMap["metadata"].(map[string]interface{}); ok {
		if annotations, ok := metadata["annotations"].(map[string]string); ok {
			if instanceName, ok := annotations["node.alpha.kubernetes.io/instance-name"]; ok {
				zone := p.zone // Use default zone
				if z, ok := annotations["topology.kubernetes.io/zone"]; ok {
					zone = z
				}
				return instanceName, zone
			}
		}
	}

	return "", ""
}

// getInstanceCost retrieves the cost for a specific instance
func (p *GCPCostProvider) getInstanceCost(instanceName, zone string) (float64, error) {
	ctx := context.Background()

	// Get instance details
	req := &computepb.GetInstanceRequest{
		Project:  p.projectID,
		Zone:     zone,
		Instance: instanceName,
	}

	instance, err := p.compute.Get(ctx, req)
	if err != nil {
		return 0, fmt.Errorf("failed to get instance: %w", err)
	}

	// Extract machine type
	machineTypeParts := strings.Split(*instance.MachineType, "/")
	machineType := machineTypeParts[len(machineTypeParts)-1]

	// Check cache first
	cacheKey := fmt.Sprintf("%s-%s", machineType, zone)
	if p.cacheExpiry.After(time.Now()) {
		if cachedPrice, ok := p.cache[cacheKey]; ok {
			return cachedPrice, nil
		}
	}

	// Get pricing for the machine type
	hourlyRate, err := p.getMachineTypePricing(machineType, zone)
	if err != nil {
		return 0, fmt.Errorf("failed to get pricing: %w", err)
	}

	// Cache the result
	p.cache[cacheKey] = hourlyRate
	p.cacheExpiry = time.Now().Add(p.cacheDuration)

	// Calculate monthly rate
	monthlyRate := hourlyRate * 24 * 30

	return monthlyRate, nil
}

// getMachineTypePricing retrieves pricing information for a machine type
func (p *GCPCostProvider) getMachineTypePricing(machineType, zone string) (float64, error) {
	// Extract region from zone
	zoneParts := strings.Split(zone, "-")
	if len(zoneParts) < 3 {
		return 0, fmt.Errorf("invalid zone format: %s", zone)
	}
	region := strings.Join(zoneParts[:2], "-")

	// Get pricing catalog
	skus, err := p.billing.Services.Skus.List("services/6F81-5844-456A").Do()
	if err != nil {
		return 0, fmt.Errorf("failed to get pricing catalog: %w", err)
	}

	// Parse machine type properties
	cpus, memory, err := p.parseMachineType(machineType)
	if err != nil {
		return 0, fmt.Errorf("failed to parse machine type: %w", err)
	}

	// Find matching SKUs for CPU and memory
	var cpuPrice, memoryPrice float64

	for _, sku := range skus.Skus {
		if !p.skuMatchesRegion(sku, region) {
			continue
		}

		if strings.Contains(sku.Description, "CPU") && strings.Contains(sku.Description, "running") {
			if len(sku.PricingInfo) > 0 && len(sku.PricingInfo[0].PricingExpression.TieredRates) > 0 {
				rate := sku.PricingInfo[0].PricingExpression.TieredRates[0]
				if rate.UnitPrice.Units != nil {
					cpuPrice = float64(*rate.UnitPrice.Units)
				}
				if rate.UnitPrice.Nanos != nil {
					cpuPrice += float64(*rate.UnitPrice.Nanos) / 1e9
				}
			}
		}

		if strings.Contains(sku.Description, "RAM") && strings.Contains(sku.Description, "running") {
			if len(sku.PricingInfo) > 0 && len(sku.PricingInfo[0].PricingExpression.TieredRates) > 0 {
				rate := sku.PricingInfo[0].PricingExpression.TieredRates[0]
				if rate.UnitPrice.Units != nil {
					memoryPrice = float64(*rate.UnitPrice.Units)
				}
				if rate.UnitPrice.Nanos != nil {
					memoryPrice += float64(*rate.UnitPrice.Nanos) / 1e9
				}
			}
		}
	}

	// Calculate total hourly cost
	hourlyRate := (cpus * cpuPrice) + (memory * memoryPrice)

	return hourlyRate, nil
}

// parseMachineType extracts CPU and memory from machine type name
func (p *GCPCostProvider) parseMachineType(machineType string) (float64, float64, error) {
	// Define standard machine types
	machineTypes := map[string]struct {
		cpu    float64
		memory float64
	}{
		"e2-micro":       {0.5, 1},
		"e2-small":       {1, 2},
		"e2-medium":      {2, 4},
		"e2-standard-2":  {2, 8},
		"e2-standard-4":  {4, 16},
		"e2-standard-8":  {8, 32},
		"e2-standard-16": {16, 64},
		"n1-standard-1":  {1, 3.75},
		"n1-standard-2":  {2, 7.5},
		"n1-standard-4":  {4, 15},
		"n1-standard-8":  {8, 30},
		"n1-standard-16": {16, 60},
		"n1-standard-32": {32, 120},
		"n1-highmem-2":   {2, 13},
		"n1-highmem-4":   {4, 26},
		"n1-highmem-8":   {8, 52},
		"n1-highmem-16":  {16, 104},
		"n1-highcpu-16":  {16, 14.4},
		"n1-highcpu-32":  {32, 28.8},
		"n1-highcpu-64":  {64, 57.6},
	}

	if specs, ok := machineTypes[machineType]; ok {
		return specs.cpu, specs.memory, nil
	}

	// For custom machine types, parse the format
	if strings.Contains(machineType, "custom-") {
		parts := strings.Split(machineType, "-")
		if len(parts) >= 4 {
			var cpu, memory float64
			if _, err := fmt.Sscanf(parts[2], "%f", &cpu); err == nil {
				if _, err := fmt.Sscanf(parts[3], "%f", &memory); err == nil {
					return cpu, memory / 1024, nil // Convert MB to GB
				}
			}
		}
	}

	return 0, 0, fmt.Errorf("unknown machine type: %s", machineType)
}

// skuMatchesRegion checks if a SKU is available in the specified region
func (p *GCPCostProvider) skuMatchesRegion(sku *cloudbilling.Sku, region string) bool {
	for _, serviceRegion := range sku.ServiceRegions {
		if serviceRegion == region {
			return true
		}
	}
	return false
}

// EstimateOptimizationSavings estimates cost savings for optimization recommendations
func (p *GCPCostProvider) EstimateOptimizationSavings(optimization map[string]interface{}) (float64, error) {
	optimizationType, ok := optimization["type"].(string)
	if !ok {
		return 0, fmt.Errorf("optimization type not found")
	}

	switch optimizationType {
	case "rightsizing":
		return p.estimateRightsizingSavings(optimization)
	case "preemptible":
		return p.estimatePreemptibleSavings(optimization)
	case "committed_use":
		return p.estimateCommittedUseSavings(optimization)
	default:
		return 0, fmt.Errorf("unknown optimization type: %s", optimizationType)
	}
}

// estimateRightsizingSavings calculates savings from machine type rightsizing
func (p *GCPCostProvider) estimateRightsizingSavings(optimization map[string]interface{}) (float64, error) {
	currentType, ok := optimization["current_machine_type"].(string)
	if !ok {
		return 0, fmt.Errorf("current machine type not specified")
	}

	recommendedType, ok := optimization["recommended_machine_type"].(string)
	if !ok {
		return 0, fmt.Errorf("recommended machine type not specified")
	}

	zone, ok := optimization["zone"].(string)
	if !ok {
		zone = p.zone // Use default zone
	}

	// Get current cost
	currentCost, err := p.getMachineTypePricing(currentType, zone)
	if err != nil {
		return 0, fmt.Errorf("failed to get current machine pricing: %w", err)
	}

	// Get recommended cost
	recommendedCost, err := p.getMachineTypePricing(recommendedType, zone)
	if err != nil {
		return 0, fmt.Errorf("failed to get recommended machine pricing: %w", err)
	}

	// Calculate monthly savings
	monthlySavings := (currentCost - recommendedCost) * 24 * 30
	return monthlySavings, nil
}

// estimatePreemptibleSavings calculates savings from using preemptible instances
func (p *GCPCostProvider) estimatePreemptibleSavings(optimization map[string]interface{}) (float64, error) {
	// Preemptible instances save up to 80% compared to regular instances
	preemptibleSavingsRate := 0.75 // 75% savings

	onDemandCost, ok := optimization["current_monthly_cost"].(float64)
	if !ok {
		return 0, fmt.Errorf("current monthly cost not specified")
	}
	preemptibleCost, err := p.getMachineTypePricing("preemptible", p.zone)
	if err != nil {
		return 0, fmt.Errorf("failed to get preemptible machine pricing: %w", err)
	}
	monthlySavings := (onDemandCost - preemptibleCost) * preemptibleSavingsRate
	return monthlySavings, nil
}

// estimateCommittedUseSavings calculates savings from committed use discounts
func (p *GCPCostProvider) estimateCommittedUseSavings(optimization map[string]interface{}) (float64, error) {
	// Committed use discounts can save up to 70% compared to on-demand pricing
	committedUseSavingsRate := 0.7 // 70% savings

	onDemandCost, ok := optimization["current_monthly_cost"].(float64)
	if !ok {
		return 0, fmt.Errorf("current monthly cost not specified")
	}
	committedUseCost, err := p.getMachineTypePricing("committed_use", p.zone)
	if err != nil {
		return 0, fmt.Errorf("failed to get committed use machine pricing: %w", err)
	}
	monthlySavings := (onDemandCost - committedUseCost) * committedUseSavingsRate
	return monthlySavings, nil
}

// getGCPProjectID retrieves the GCP project ID from metadata or environment
func getGCPProjectID() string {
	// This is a placeholder function. In a real implementation, you would
	// retrieve the project ID from GCP metadata or environment variables.
	return "your-gcp-project-id"
}

// Close cleans up resources
func (p *GCPCostProvider) Close() {
	if p.compute != nil {
		p.compute.Close()
	}
	if p.billing != nil {
		p.billing.
	}
}

// GetProjectID returns the project ID
func (p *GCPCostProvider) GetProjectID() string {
	return p.projectID
}

// GetZone returns the zone
func (p *GCPCostProvider) GetZone() string {
	return p.zone
}

// GetCache returns the cache
func (p *GCPCostProvider) GetCache() map[string]float64 {
	return p.cache
}

// GetCacheExpiry returns the cache expiry time
func (p *GCPCostProvider) GetCacheExpiry() time.Time {
	return p.cacheExpiry
}

// GetCacheDuration returns the cache duration
func (p *GCPCostProvider) GetCacheDuration() time.Duration {
	return p.cacheDuration
}

// SetCacheDuration sets the cache duration
func (p *GCPCostProvider) SetCacheDuration(duration time.Duration) {
	p.cacheDuration = duration
}

// SetCacheExpiry sets the cache expiry time
func (p *GCPCostProvider) SetCacheExpiry(expiry time.Time) {
	p.cacheExpiry = expiry
}

// SetCache sets the cache
func (p *GCPCostProvider) SetCache(cache map[string]float64) {
	p.cache = cache
}

// SetProjectID sets the project ID
func (p *GCPCostProvider) SetProjectID(projectID string) {
	p.projectID = projectID
}

// SetZone sets the zone
func (p *GCPCostProvider) SetZone(zone string) {
	p.zone = zone
}

// SetComputeClient sets the compute client
func (p *GCPCostProvider) SetComputeClient(client *compute.InstancesClient) {
	p.compute = client
}

// SetBillingClient sets the billing client
func (p *GCPCostProvider) SetBillingClient(client *cloudbilling.Service) {
	p.billing = client
}

// SetCatalogService sets the billing catalog service
func (p *GCPCostProvider) SetCatalogService(client *billingcatalogmanagement.Service) {
	p.catalogSvc = client
}

// SetCacheExpiry sets the cache expiry time
func (p *GCPCostProvider) SetCacheExpiry(expiry time.Time) {
	p.cacheExpiry = expiry
}

// SetCacheDuration sets the cache duration
func (p *GCPCostProvider) SetCacheDuration(duration time.Duration) {
	p.cacheDuration = duration
}
