package providers

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement"
)

// AzureCostProvider implements cost retrieval for Azure resources
type AzureCostProvider struct {
	vmClient          *armcompute.VirtualMachinesClient
	consumptionClient *armconsumption.PriceSheetClient
	costClient        *armcostmanagement.QueryClient
	credential        *azidentity.DefaultAzureCredential
	subscriptionID    string
	resourceGroup     string
	cache             map[string]float64
	cacheExpiry       time.Time
	cacheDuration     time.Duration
}

// AzureResourceCost represents the cost structure for Azure resources
type AzureResourceCost struct {
	ResourceID   string
	ResourceType string
	VMSize       string
	HourlyRate   float64
	MonthlyRate  float64
	Location     string
	Tags         map[string]*string
}

// NewAzureCostProvider creates a new Azure cost provider
func NewAzureCostProvider(endpoint string) *AzureCostProvider {
	// Get Azure credentials using DefaultAzureCredential
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Fatalf("Failed to create Azure credential: %v", err)
	}

	// Get subscription ID from environment
	subscriptionID := getAzureSubscriptionID()

	// Create VM client
	vmClient, err := armcompute.NewVirtualMachinesClient(subscriptionID, credential, nil)
	if err != nil {
		log.Fatalf("Failed to create VM client: %v", err)
	}

	// Create consumption client
	consumptionClient, err := armconsumption.NewPriceSheetClient(subscriptionID, credential, nil)
	if err != nil {
		log.Fatalf("Failed to create consumption client: %v", err)
	}

	// Create cost management client
	costClient, err := armcostmanagement.NewQueryClient(credential, nil)
	if err != nil {
		log.Fatalf("Failed to create cost management client: %v", err)
	}

	return &AzureCostProvider{
		vmClient:          vmClient,
		consumptionClient: consumptionClient,
		costClient:        costClient,
		credential:        credential,
		subscriptionID:    subscriptionID,
		cache:             make(map[string]float64),
		cacheExpiry:       time.Now(),
		cacheDuration:     24 * time.Hour,
	}
}

// GetResourceCosts retrieves costs for resources in the cluster
func (p *AzureCostProvider) GetResourceCosts(resources map[string]interface{}) (map[string]float64, error) {
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

		// Extract VM details from node metadata
		vmName, resourceGroup := p.extractVMInfo(nodeMap)
		if vmName == "" {
			continue
		}

		// Get VM details and cost
		cost, err := p.getVMCost(vmName, resourceGroup)
		if err != nil {
			log.Printf("Failed to get cost for VM %s: %v", vmName, err)
			continue
		}

		costs[nodeName] = cost
	}

	return costs, nil
}

// extractVMInfo extracts Azure VM name and resource group from node metadata
func (p *AzureCostProvider) extractVMInfo(nodeMap map[string]interface{}) (string, string) {
	// Try to get provider ID first
	if status, ok := nodeMap["status"].(map[string]interface{}); ok {
		if nodeInfo, ok := status["nodeInfo"].(map[string]interface{}); ok {
			if providerID, ok := nodeInfo["machineID"].(string); ok {
				// Provider ID format: azure:///subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Compute/virtualMachines/{name}
				parts := strings.Split(providerID, "/")
				for i, part := range parts {
					if part == "resourceGroups" && i+1 < len(parts) {
						resourceGroup := parts[i+1]
						if i+4 < len(parts) && parts[i+3] == "virtualMachines" {
							vmName := parts[i+4]
							return vmName, resourceGroup
						}
					}
				}
			}
		}
	}

	// Try to get from annotations
	if metadata, ok := nodeMap["metadata"].(map[string]interface{}); ok {
		if annotations, ok := metadata["annotations"].(map[string]string); ok {
			if vmName, ok := annotations["kubernetes.azure.com/node-resourceid"]; ok {
				// Extract resource group from resource ID
				parts := strings.Split(vmName, "/")
				for i, part := range parts {
					if part == "resourceGroups" && i+1 < len(parts) {
						resourceGroup := parts[i+1]
						if i+4 < len(parts) && parts[i+3] == "virtualMachines" {
							vm := parts[i+4]
							return vm, resourceGroup
						}
					}
				}
			}
		}
	}

	return "", ""
}

// getVMCost retrieves the cost for a specific VM
func (p *AzureCostProvider) getVMCost(vmName, resourceGroup string) (float64, error) {
	ctx := context.Background()

	// Get VM details
	resp, err := p.vmClient.Get(ctx, resourceGroup, vmName, &armcompute.VirtualMachinesClientGetOptions{
		Expand: nil,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get VM: %w", err)
	}

	vm := resp.VirtualMachine

	// Extract VM size and location
	vmSize := *vm.Properties.HardwareProfile.VMSize
	location := *vm.Location

	// Check cache first
	cacheKey := fmt.Sprintf("%s-%s", string(vmSize), location)
	if p.cacheExpiry.After(time.Now()) {
		if cachedPrice, ok := p.cache[cacheKey]; ok {
			return cachedPrice, nil
		}
	}

	// Get pricing for the VM size
	hourlyRate, err := p.getVMPricing(vmSize, location)
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

// getVMPricing retrieves pricing information for a VM size
func (p *AzureCostProvider) getVMPricing(vmSize armcompute.VirtualMachineSizeTypes, location string) (float64, error) {
	ctx := context.Background()

	// Get price sheet
	resp, err := p.consumptionClient.Get(ctx, &armconsumption.PriceSheetClientGetOptions{
		Expand:    nil,
		Skiptoken: nil,
		Top:       nil,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get price sheet: %w", err)
	}

	priceSheet := resp.PriceSheetResult

	// Find matching price for VM size
	if priceSheet.Properties != nil && priceSheet.Properties.Pricesheets != nil {
		for _, priceItem := range priceSheet.Properties.Pricesheets {
			if priceItem.MeterDetails == nil || priceItem.MeterId == nil || priceItem.UnitPrice == nil {
				continue
			}

			// Check if this is a VM price for the specified size
			if priceItem.MeterDetails.MeterCategory != nil &&
				*priceItem.MeterDetails.MeterCategory == "Virtual Machines" &&
				priceItem.MeterDetails.MeterName != nil &&
				strings.Contains(*priceItem.MeterDetails.MeterName, string(vmSize)) {

				// Check if location matches
				if priceItem.MeterDetails.MeterSubCategory != nil &&
					strings.Contains(*priceItem.MeterDetails.MeterSubCategory, location) {
					return *priceItem.UnitPrice, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("no pricing found for VM size %s", vmSize)
}

// GetCostByResource retrieves cost by resource using Cost Management API
func (p *AzureCostProvider) GetCostByResource(timespan string) (map[string]float64, error) {
	ctx := context.Background()

	// Calculate date range
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -30) // Last 30 days

	// Create enum value pointers correctly
	exportType := armcostmanagement.ExportTypeActualCost
	timeframeType := armcostmanagement.TimeframeTypeCustom
	granularityType := armcostmanagement.GranularityTypeDaily
	functionType := armcostmanagement.FunctionTypeSum
	queryColumnType := armcostmanagement.QueryColumnTypeDimension

	// Prepare query
	query := armcostmanagement.QueryDefinition{
		Type:      &exportType,
		Timeframe: &timeframeType,
		TimePeriod: &armcostmanagement.QueryTimePeriod{
			From: &startDate,
			To:   &endDate,
		},
		Dataset: &armcostmanagement.QueryDataset{
			Granularity: &granularityType,
			Aggregation: map[string]*armcostmanagement.QueryAggregation{
				"totalCost": {
					Name:     stringPtr("Cost"),
					Function: &functionType,
				},
			},
			Grouping: []*armcostmanagement.QueryGrouping{
				{
					Type: &queryColumnType,
					Name: stringPtr("ResourceId"),
				},
			},
		},
	}

	// Build scope
	scope := fmt.Sprintf("/subscriptions/%s", p.subscriptionID)

	// Execute query
	resp, err := p.costClient.Usage(ctx, scope, query, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query cost: %w", err)
	}

	result := resp.QueryResult

	// Parse results
	costs := make(map[string]float64)
	if result.Properties != nil && result.Properties.Rows != nil {
		for _, row := range result.Properties.Rows {
			if len(row) >= 2 {
				if resourceID, ok := row[0].(string); ok {
					if cost, ok := row[1].(float64); ok {
						costs[resourceID] = cost
					}
				}
			}
		}
	}

	return costs, nil
}

// EstimateOptimizationSavings estimates cost savings for optimization recommendations
func (p *AzureCostProvider) EstimateOptimizationSavings(optimization map[string]interface{}) (float64, error) {
	optimizationType, ok := optimization["type"].(string)
	if !ok {
		return 0, fmt.Errorf("optimization type not found")
	}

	switch optimizationType {
	case "rightsizing":
		return p.estimateRightsizingSavings(optimization)
	case "spot_vm":
		return p.estimateSpotVMSavings(optimization)
	case "reserved_instance":
		return p.estimateReservedInstanceSavings(optimization)
	case "autoscaling":
		return p.estimateAutoscalingSavings(optimization)
	default:
		return 0, fmt.Errorf("unknown optimization type: %s", optimizationType)
	}
}

// estimateRightsizingSavings calculates savings from VM rightsizing
func (p *AzureCostProvider) estimateRightsizingSavings(optimization map[string]interface{}) (float64, error) {
	currentSize, ok := optimization["current_vm_size"].(string)
	if !ok {
		return 0, fmt.Errorf("current VM size not specified")
	}

	recommendedSize, ok := optimization["recommended_vm_size"].(string)
	if !ok {
		return 0, fmt.Errorf("recommended VM size not specified")
	}

	location, ok := optimization["location"].(string)
	if !ok {
		location = "eastus" // Default location
	}

	// Get current cost
	currentCost, err := p.getVMPricing(armcompute.VirtualMachineSizeTypes(currentSize), location)
	if err != nil {
		return 0, fmt.Errorf("failed to get current VM pricing: %w", err)
	}

	// Get recommended cost
	recommendedCost, err := p.getVMPricing(armcompute.VirtualMachineSizeTypes(recommendedSize), location)
	if err != nil {
		return 0, fmt.Errorf("failed to get recommended VM pricing: %w", err)
	}

	// Calculate monthly savings
	monthlySavings := (currentCost - recommendedCost) * 24 * 30
	return monthlySavings, nil
}

// estimateSpotVMSavings calculates savings from using Spot VMs
func (p *AzureCostProvider) estimateSpotVMSavings(optimization map[string]interface{}) (float64, error) {
	// Spot VMs typically save 60-90% compared to pay-as-you-go prices
	spotSavingsRate := 0.70 // 70% savings

	onDemandCost, ok := optimization["current_monthly_cost"].(float64)
	if !ok {
		return 0, fmt.Errorf("current monthly cost not specified")
	}

	spotCost := onDemandCost * (1 - spotSavingsRate)
	monthlySavings := onDemandCost - spotCost

	return monthlySavings, nil
}

// estimateReservedInstanceSavings calculates savings from reserved instances
func (p *AzureCostProvider) estimateReservedInstanceSavings(optimization map[string]interface{}) (float64, error) {
	// Reserved instances typically save 40-75% depending on term
	term, ok := optimization["reservation_term"].(string)
	if !ok {
		term = "1year" // Default to 1 year
	}

	var savingsRate float64
	switch term {
	case "1year":
		savingsRate = 0.40 // 40% savings
	case "3year":
		savingsRate = 0.60 // 60% savings
	default:
		savingsRate = 0.40
	}

	onDemandCost, ok := optimization["current_monthly_cost"].(float64)
	if !ok {
		return 0, fmt.Errorf("current monthly cost not specified")
	}

	reservedCost := onDemandCost * (1 - savingsRate)
	monthlySavings := onDemandCost - reservedCost

	return monthlySavings, nil
}

// estimateAutoscalingSavings calculates savings from VM autoscaling
func (p *AzureCostProvider) estimateAutoscalingSavings(optimization map[string]interface{}) (float64, error) {
	// Autoscaling typically saves 20-40% during off-peak hours
	savingsRate := 0.30 // 30% average savings

	currentCost, ok := optimization["current_monthly_cost"].(float64)
	if !ok {
		return 0, fmt.Errorf("current monthly cost not specified")
	}

	scaledCost := currentCost * (1 - savingsRate)
	monthlySavings := currentCost - scaledCost

	return monthlySavings, nil
}

// getAzureSubscriptionID retrieves the Azure subscription ID
func getAzureSubscriptionID() string {
	// Try to get from environment variable
	if subscriptionID := os.Getenv("AZURE_SUBSCRIPTION_ID"); subscriptionID != "" {
		return subscriptionID
	}

	// Default if not found
	return "default-subscription"
}

// ValidateCredentials validates Azure credentials
func (p *AzureCostProvider) ValidateCredentials() error {
	ctx := context.Background()

	// Test VM client by listing VMs (limited query)
	_, err := p.vmClient.NewListAllPager(nil).NextPage(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate Azure credentials: %w", err)
	}

	return nil
}

// stringPtr is a helper function to get a pointer to a string
func stringPtr(s string) *string {
	return &s
}
