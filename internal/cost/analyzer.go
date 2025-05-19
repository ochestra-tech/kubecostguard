package cost

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/config"
	"github.com/ochestra-tech/kubecostguard/internal/cost/providers"
	"github.com/ochestra-tech/kubecostguard/internal/cost/recommender"
	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"
)

// CostProvider interface defines the methods that all cost providers must implement
type CostProvider interface {
	GetResourceCosts(resources map[string]interface{}) (map[string]float64, error)
	EstimateOptimizationSavings(optimization map[string]interface{}) (float64, error)
	ValidateCredentials() error
}

// NamespaceCostData represents cost data for a namespace
type NamespaceCostData struct {
	TotalCost    float64
	CPUCost      float64
	MemoryCost   float64
	StorageCost  float64
	NetworkCost  float64
	PodCount     int
	CPUUsage     float64
	MemoryUsage  float64
	StorageUsage float64
}

// NodeCostData represents cost data for a node
type NodeCostData struct {
	TotalCost         float64
	CPUCost           float64
	MemoryCost        float64
	InstanceType      string
	Region            string
	CPUUtilization    float64
	MemoryUtilization float64
	PodCount          int
}

// DeploymentCostData represents cost data for a deployment
type DeploymentCostData struct {
	TotalCost        float64
	CPUCost          float64
	MemoryCost       float64
	StorageCost      float64
	ReplicaCount     int
	AvgPodCost       float64
	RecommendedScale int
}

// CostTrendPoint represents a cost data point over time
type CostTrendPoint struct {
	Date string
	Cost float64
}

// Analyzer handles cost analysis for Kubernetes resources
type Analyzer struct {
	ctx          context.Context
	k8sClient    *kubernetes.Client
	config       config.CostConfig
	costProvider providers.CostProvider
	recommenders []recommender.Recommender
	costData     map[string]float64
	costHistory  []map[string]float64
	costDataMu   sync.RWMutex
	historyMu    sync.RWMutex
	stopChan     chan struct{}
	recCollector *recommender.RecommendationsCollector
}

// NewAnalyzer creates a new cost analyzer
func NewAnalyzer(ctx context.Context, k8sClient *kubernetes.Client, config config.CostConfig) *Analyzer {
	a := &Analyzer{
		ctx:         ctx,
		k8sClient:   k8sClient,
		config:      config,
		costData:    make(map[string]float64),
		costHistory: make([]map[string]float64, 0),
		stopChan:    make(chan struct{}),
	}

	// Initialize cost provider based on configuration
	a.initCostProvider()

	// Initialize recommenders
	a.initRecommenders()

	return a
}

// initCostProvider sets up the cloud provider cost integration
func (a *Analyzer) initCostProvider() {
	switch a.config.CloudProvider {
	case "aws":
		a.costProvider = providers.NewAWSCostProvider(a.config.PricingAPIEndpoint)
	case "gcp":
		a.costProvider = providers.NewGCPCostProvider(a.config.PricingAPIEndpoint)
	case "azure":
		a.costProvider = providers.NewAzureCostProvider(a.config.PricingAPIEndpoint)
	default:
		log.Printf("Using default cost provider for cloud provider: %s", a.config.CloudProvider)
		a.costProvider = providers.NewDefaultCostProvider()
	}
}

// initRecommenders sets up the cost optimization recommenders
func (a *Analyzer) initRecommenders() {
	a.recommenders = append(a.recommenders,
		recommender.NewRightsizingRecommender(a.k8sClient, a.costProvider),
		recommender.NewIdleResourceRecommender(a.k8sClient),
	)
}

// Start begins the cost analysis process
func (a *Analyzer) Start() error {
	// Start cost update loop
	go a.updateLoop()

	return nil
}

// updateLoop periodically updates cost data
func (a *Analyzer) updateLoop() {
	interval := time.Duration(a.config.UpdateIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial update
	a.updateCostData()

	for {
		select {
		case <-ticker.C:
			a.updateCostData()
		case <-a.stopChan:
			return
		case <-a.ctx.Done():
			return
		}
	}
}

// updateCostData refreshes cost information
func (a *Analyzer) updateCostData() {
	// Get current cluster resources
	resources, err := a.k8sClient.GetClusterResources()
	if err != nil {
		log.Printf("Error getting cluster resources: %v", err)
		return
	}

	// Get cost data from provider
	costs, err := a.costProvider.GetResourceCosts(resources)
	if err != nil {
		log.Printf("Error getting resource costs: %v", err)
		return
	}

	// Update cost data
	a.costDataMu.Lock()
	a.costData = costs
	a.costDataMu.Unlock()

	// Add to history
	a.addToHistory(costs)

	// Generate recommendations
	for _, rec := range a.recommenders {
		recommendations, err := rec.GenerateRecommendations(resources, costs)
		if err != nil {
			log.Printf("Error generating recommendations from %s: %v", rec.Name(), err)
			continue
		}
		recs, ok := recommendations.([]interface{})
		if !ok {
			log.Printf("Error: unexpected recommendations type from %s", rec.Name())
			continue
		}
		log.Printf("Generated %d recommendations from %s", len(recs), rec.Name())
	}
}

// GetCostData returns the current cost data
func (a *Analyzer) GetCostData() map[string]float64 {
	a.costDataMu.RLock()
	defer a.costDataMu.RUnlock()

	// Create a copy of the cost data
	result := make(map[string]float64, len(a.costData))
	for k, v := range a.costData {
		result[k] = v
	}

	return result
}

// GetNamespaceCosts returns cost data grouped by namespace
func (a *Analyzer) GetNamespaceCosts() (map[string]*NamespaceCostData, error) {
	// Get current cost data
	costs := a.GetCostData()

	// Get cluster resources
	resources, err := a.k8sClient.GetClusterResources()
	if err != nil {
		return nil, err
	}

	// Extract pods for namespace analysis
	pods, ok := resources["pods"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to extract pods from resources")
	}

	// Create namespace cost map
	namespaceCosts := make(map[string]*NamespaceCostData)

	// Process each pod
	for _, pod := range pods {
		podMap, ok := pod.(map[string]interface{})
		if !ok {
			continue
		}

		metadata, ok := podMap["metadata"].(map[string]interface{})
		if !ok {
			continue
		}

		namespace, ok := metadata["namespace"].(string)
		if !ok {
			continue
		}

		// Initialize namespace data if not exists
		if _, ok := namespaceCosts[namespace]; !ok {
			namespaceCosts[namespace] = &NamespaceCostData{}
		}

		// Get pod's node
		spec, ok := podMap["spec"].(map[string]interface{})
		if !ok {
			continue
		}

		nodeName, ok := spec["nodeName"].(string)
		if !ok {
			continue
		}

		// Add node cost to namespace (distributed among pods)
		if nodeCost, ok := costs[nodeName]; ok {
			// Simplified: distribute node cost equally among pods
			// In reality, you'd want to distribute based on resource requests
			namespaceCosts[namespace].TotalCost += nodeCost / 10 // Assuming ~10 pods per node
		}

		namespaceCosts[namespace].PodCount++
	}

	return namespaceCosts, nil
}

// GetNamespaceCostDetails returns detailed cost information for a namespace
func (a *Analyzer) GetNamespaceCostDetails(namespace string) (map[string]interface{}, error) {
	resources, err := a.k8sClient.GetClusterResources()
	if err != nil {
		return nil, err
	}

	details := make(map[string]interface{})

	// Extract pods for the namespace
	pods, ok := resources["pods"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to extract pods from resources")
	}

	var totalCost float64
	podCosts := make([]map[string]interface{}, 0)

	for _, pod := range pods {
		podMap, ok := pod.(map[string]interface{})
		if !ok {
			continue
		}

		metadata, ok := podMap["metadata"].(map[string]interface{})
		if !ok {
			continue
		}

		podNamespace, ok := metadata["namespace"].(string)
		if !ok || podNamespace != namespace {
			continue
		}

		// Get pod cost (simplified)
		podCost := a.calculatePodCost(podMap)
		totalCost += podCost

		podCosts = append(podCosts, map[string]interface{}{
			"name": metadata["name"],
			"cost": podCost,
		})
	}

	details["namespace"] = namespace
	details["total_cost"] = totalCost
	details["pod_costs"] = podCosts

	return details, nil
}

// GetNodeCosts returns cost data grouped by node
func (a *Analyzer) GetNodeCosts() (map[string]*NodeCostData, error) {
	costs := a.GetCostData()
	nodeCosts := make(map[string]*NodeCostData)

	resources, err := a.k8sClient.GetClusterResources()
	if err != nil {
		return nil, err
	}

	nodes, ok := resources["nodes"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("failed to extract nodes from resources")
	}

	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		metadata, ok := nodeMap["metadata"].(map[string]interface{})
		if !ok {
			continue
		}

		nodeName, ok := metadata["name"].(string)
		if !ok {
			continue
		}

		nodeData := &NodeCostData{}

		if cost, ok := costs[nodeName]; ok {
			nodeData.TotalCost = cost
		}

		// Extract additional node information
		if labels, ok := metadata["labels"].(map[string]string); ok {
			nodeData.InstanceType = labels["node.kubernetes.io/instance-type"]
			nodeData.Region = labels["topology.kubernetes.io/zone"]
		}

		nodeCosts[nodeName] = nodeData
	}

	return nodeCosts, nil
}

// GetNodeCostDetails returns detailed cost information for a node
func (a *Analyzer) GetNodeCostDetails(nodeName string) (map[string]interface{}, error) {
	costs := a.GetCostData()
	details := make(map[string]interface{})

	if cost, ok := costs[nodeName]; ok {
		details["name"] = nodeName
		details["total_cost"] = cost

		// Get pods on this node
		resources, err := a.k8sClient.GetClusterResources()
		if err != nil {
			return nil, err
		}

		pods, ok := resources["pods"].([]interface{})
		if ok {
			podCount := 0
			for _, pod := range pods {
				podMap, ok := pod.(map[string]interface{})
				if !ok {
					continue
				}

				spec, ok := podMap["spec"].(map[string]interface{})
				if !ok {
					continue
				}

				if nodeNameSpec, ok := spec["nodeName"].(string); ok && nodeNameSpec == nodeName {
					podCount++
				}
			}
			details["pod_count"] = podCount
		}
	}

	return details, nil
}

// GetDeploymentCosts returns cost data grouped by deployment
func (a *Analyzer) GetDeploymentCosts() (map[string]*DeploymentCostData, error) {
	_, err := a.k8sClient.GetClusterResources()
	if err != nil {
		return nil, err
	}

	deploymentCosts := make(map[string]*DeploymentCostData)

	// This would require iterating through pods and grouping by owner references
	// Simplified implementation
	deploymentCosts["example-deployment"] = &DeploymentCostData{
		TotalCost:    100.0,
		ReplicaCount: 3,
		AvgPodCost:   33.3,
	}

	return deploymentCosts, nil
}

// GetDeploymentCostDetails returns detailed cost information for a deployment
func (a *Analyzer) GetDeploymentCostDetails(deploymentName, namespace string) (map[string]interface{}, error) {
	details := make(map[string]interface{})
	details["deployment"] = deploymentName
	details["namespace"] = namespace
	details["total_cost"] = 100.0

	return details, nil
}

// GetCostTrends returns cost trends over time
func (a *Analyzer) GetCostTrends(duration string) ([]CostTrendPoint, error) {
	a.historyMu.RLock()
	defer a.historyMu.RUnlock()

	trends := make([]CostTrendPoint, 0)

	// Parse duration and extract relevant history
	// Simplified implementation
	for i, history := range a.costHistory {
		var totalCost float64
		for _, cost := range history {
			totalCost += cost
		}

		trends = append(trends, CostTrendPoint{
			Date: time.Now().AddDate(0, 0, -len(a.costHistory)+i).Format("2006-01-02"),
			Cost: totalCost,
		})
	}

	return trends, nil
}

// GetCostForecast returns cost forecast
func (a *Analyzer) GetCostForecast(duration string) ([]CostTrendPoint, error) {
	// Get current trends
	trends, err := a.GetCostTrends("30d")
	if err != nil {
		return nil, err
	}

	// Simple linear forecast
	forecast := make([]CostTrendPoint, 0)

	if len(trends) >= 2 {
		// Calculate growth rate
		firstCost := trends[0].Cost
		lastCost := trends[len(trends)-1].Cost
		growthRate := (lastCost - firstCost) / firstCost

		// Forecast next 30 days
		for i := 1; i <= 30; i++ {
			forecastCost := lastCost * (1 + growthRate/30*float64(i))
			forecast = append(forecast, CostTrendPoint{
				Date: time.Now().AddDate(0, 0, i).Format("2006-01-02"),
				Cost: forecastCost,
			})
		}
	}

	return forecast, nil
}

// GenerateReport generates a cost report in the specified format
func (a *Analyzer) GenerateReport(format string) (interface{}, error) {
	costs := a.GetCostData()

	switch format {
	case "json":
		return costs, nil
	case "csv":
		// Generate CSV format
		csv := "Resource,Cost\n"
		for resource, cost := range costs {
			csv += fmt.Sprintf("%s,%.2f\n", resource, cost)
		}
		return csv, nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// addToHistory adds cost data to history
func (a *Analyzer) addToHistory(costs map[string]float64) {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()

	// Create a copy of the costs
	historyCosts := make(map[string]float64, len(costs))
	for k, v := range costs {
		historyCosts[k] = v
	}

	a.costHistory = append(a.costHistory, historyCosts)

	// Keep only last 90 days of history
	maxHistory := 90
	if len(a.costHistory) > maxHistory {
		a.costHistory = a.costHistory[len(a.costHistory)-maxHistory:]
	}
}

// calculatePodCost estimates the cost of a pod
func (a *Analyzer) calculatePodCost(podMap map[string]interface{}) float64 {
	// Simplified cost calculation
	// In reality, you'd calculate based on resource requests and node costs
	return 10.0 // Example: $10 per pod
}

// Stop stops the cost analysis process
func (a *Analyzer) Stop() {
	close(a.stopChan)
}
