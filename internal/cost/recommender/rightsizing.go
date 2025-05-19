// File: internal/cost/recommender/rightsizing.go
package recommender

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/cost/providers"
	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceUtilization represents the utilization metrics for a resource
type ResourceUtilization struct {
	CPUUtilization    float64
	MemoryUtilization float64
	CPURequest        resource.Quantity
	MemoryRequest     resource.Quantity
	CPULimit          resource.Quantity
	MemoryLimit       resource.Quantity
	ActualCPU         resource.Quantity
	ActualMemory      resource.Quantity
	PodCount          int
}

// RightsizingRecommendation represents a recommendation to right-size resources
type RightsizingRecommendation struct {
	ResourceType             string
	ResourceName             string
	CurrentConfiguration     string
	RecommendedConfiguration string
	CurrentMonthlyCost       float64
	RecommendedMonthlyCost   float64
	PotentialSavings         float64
	Confidence               float64 // 0-1 scale
	Reason                   string
	Impact                   string
	Priority                 int // 1-5, 5 being highest
	NotificationChannels     []string
}

// RightsizingRecommender generates recommendations for right-sizing resources
type RightsizingRecommender struct {
	k8sClient       *kubernetes.Client
	costProvider    providers.CostProvider
	ctx             context.Context
	metrics         map[string]*ResourceUtilization
	recommendations []*RightsizingRecommendation
	thresholds      struct {
		CPUWaste          float64       // Percentage of CPU that's allocated but not used
		MemoryWaste       float64       // Percentage of memory that's allocated but not used
		MinConfidence     float64       // Minimum confidence threshold for recommendations
		ObservationPeriod time.Duration // How long to observe metrics
	}
}

// GenerateRecommendations generates rightsizing recommendations
func (r *RightsizingRecommender) GenerateRecommendations(resources map[string]interface{}, costs map[string]float64) (interface{}, error) {
	ctx := context.Background()
	r.ctx = ctx

	// Clear previous recommendations
	r.recommendations = make([]*RightsizingRecommendation, 0)

	// Analyze node rightsizing opportunities
	if err := r.analyzeNodeRightsizing(resources, costs); err != nil {
		log.Printf("Error analyzing node rightsizing: %v", err)
	}

	// Analyze pod rightsizing opportunities
	if err := r.analyzePodRightsizing(resources, costs); err != nil {
		log.Printf("Error analyzing pod rightsizing: %v", err)
	}

	// Analyze deployment rightsizing opportunities
	if err := r.analyzeDeploymentRightsizing(resources, costs); err != nil {
		log.Printf("Error analyzing deployment rightsizing: %v", err)
	}

	// Sort recommendations by potential savings
	sort.Slice(r.recommendations, func(i, j int) bool {
		return r.recommendations[i].PotentialSavings > r.recommendations[j].PotentialSavings
	})

	return r.recommendations, nil
}

// NewRightsizingRecommender creates a new rightsizing recommender
func NewRightsizingRecommender(k8sClient *kubernetes.Client, costProvider providers.CostProvider) *RightsizingRecommender {
	return &RightsizingRecommender{
		k8sClient:       k8sClient,
		costProvider:    costProvider,
		metrics:         make(map[string]*ResourceUtilization),
		recommendations: make([]*RightsizingRecommendation, 0),
		thresholds: struct {
			CPUWaste          float64
			MemoryWaste       float64
			MinConfidence     float64
			ObservationPeriod time.Duration
		}{
			CPUWaste:          40.0,               // 40% unused CPU triggers a recommendation
			MemoryWaste:       30.0,               // 30% unused memory triggers a recommendation
			MinConfidence:     0.6,                // Only show recommendations with >60% confidence
			ObservationPeriod: 7 * 24 * time.Hour, // 7 days of metrics
		},
	}
}

// Name returns the recommender name
func (r *RightsizingRecommender) Name() string {
	return "rightsizing-recommender"
}

// analyzeNodeRightsizing analyzes nodes for rightsizing opportunities
func (r *RightsizingRecommender) analyzeNodeRightsizing(resources map[string]interface{}, costs map[string]float64) error {
	// Get nodes from resources
	nodes, ok := resources["nodes"].([]interface{})
	if !ok {
		return fmt.Errorf("failed to extract nodes from resources")
	}

	// Get node metrics
	nodeMetrics, err := r.getNodeMetrics()
	if err != nil {
		return fmt.Errorf("failed to get node metrics: %w", err)
	}

	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		nodeName, ok := nodeMap["metadata"].(map[string]interface{})["name"].(string)
		if !ok {
			continue
		}

		// Get node capacity
		capacity := r.getNodeCapacity(nodeMap)
		if capacity == nil {
			continue
		}

		// Get node utilization
		utilization, ok := nodeMetrics[nodeName]
		if !ok {
			continue
		}

		// Analyze for rightsizing opportunity
		recommendation := r.analyzeNodeUtilization(nodeName, capacity, utilization, costs[nodeName])
		if recommendation != nil {
			r.recommendations = append(r.recommendations, recommendation)
		}
	}

	return nil
}

// analyzePodRightsizing analyzes pods for rightsizing opportunities
func (r *RightsizingRecommender) analyzePodRightsizing(resources map[string]interface{}, costs map[string]float64) error {
	// Get pod metrics
	podMetrics, err := r.getPodMetrics()
	if err != nil {
		return fmt.Errorf("failed to get pod metrics: %w", err)
	}

	for podKey, metrics := range podMetrics {
		// Analyze pod utilization
		recommendation := r.analyzePodUtilization(podKey, metrics)
		if recommendation != nil {
			r.recommendations = append(r.recommendations, recommendation)
		}
	}

	return nil
}

// analyzeDeploymentRightsizing analyzes deployments for rightsizing opportunities
func (r *RightsizingRecommender) analyzeDeploymentRightsizing(resources map[string]interface{}, costs map[string]float64) error {
	// Get deployments
	deployments, err := r.k8sClient.AppsV1().Deployments("").List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	for _, deployment := range deployments.Items {
		// Aggregate metrics for deployment
		deploymentMetrics := r.aggregateDeploymentMetrics(deployment.Name, deployment.Namespace)
		if deploymentMetrics == nil {
			continue
		}

		// Analyze deployment utilization
		recommendation := r.analyzeDeploymentUtilization(deployment.Name, deployment.Namespace, deploymentMetrics)
		if recommendation != nil {
			r.recommendations = append(r.recommendations, recommendation)
		}
	}

	return nil
}

// getNodeMetrics retrieves utilization metrics for nodes
func (r *RightsizingRecommender) getNodeMetrics() (map[string]*ResourceUtilization, error) {
	// Get node metrics from metrics-server
	nodeMetrics, err := r.k8sClient.MetricsV1beta1().NodeMetricses().List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node metrics: %w", err)
	}

	// Get node details
	nodes, err := r.k8sClient.CoreV1().Nodes().List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	metrics := make(map[string]*ResourceUtilization)

	for _, nodeMetric := range nodeMetrics.Items {
		for _, node := range nodes.Items {
			if node.Name == nodeMetric.Name {
				metrics[node.Name] = &ResourceUtilization{
					ActualCPU:    nodeMetric.Usage.Cpu().DeepCopy(),
					ActualMemory: nodeMetric.Usage.Memory().DeepCopy(),
				}

				// Calculate utilization percentages
				cpuCapacity := node.Status.Capacity.Cpu()
				memoryCapacity := node.Status.Capacity.Memory()

				metrics[node.Name].CPUUtilization = float64(nodeMetric.Usage.Cpu().MilliValue()) / float64(cpuCapacity.MilliValue()) * 100
				metrics[node.Name].MemoryUtilization = float64(nodeMetric.Usage.Memory().Value()) / float64(memoryCapacity.Value()) * 100

				break
			}
		}
	}

	return metrics, nil
}

// getPodMetrics retrieves utilization metrics for pods
func (r *RightsizingRecommender) getPodMetrics() (map[string]*ResourceUtilization, error) {
	// Get pod metrics from metrics-server
	podMetrics, err := r.k8sClient.MetricsV1beta1().PodMetricses("").List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	// Get pod details
	pods, err := r.k8sClient.CoreV1().Pods("").List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	metrics := make(map[string]*ResourceUtilization)

	for _, podMetric := range podMetrics.Items {
		for _, pod := range pods.Items {
			if pod.Name == podMetric.Name && pod.Namespace == podMetric.Namespace {
				podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

				metrics[podKey] = &ResourceUtilization{
					ActualCPU:    *resource.NewMilliQuantity(0, resource.DecimalSI),
					ActualMemory: *resource.NewQuantity(0, resource.BinarySI),
				}

				// Aggregate container metrics
				for _, containerMetric := range podMetric.Containers {
					cpuUsage := metrics[podKey].ActualCPU.DeepCopy()
					cpuUsage.Add(*containerMetric.Usage.Cpu())
					metrics[podKey].ActualCPU = cpuUsage

					memoryUsage := metrics[podKey].ActualMemory.DeepCopy()
					memoryUsage.Add(*containerMetric.Usage.Memory())
					metrics[podKey].ActualMemory = memoryUsage
				}

				// Get requests and limits
				for _, container := range pod.Spec.Containers {
					if container.Resources.Requests != nil {
						if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
							metrics[podKey].CPURequest.Add(cpu)
						}
						if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
							metrics[podKey].MemoryRequest.Add(memory)
						}
					}
					if container.Resources.Limits != nil {
						if cpu, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
							metrics[podKey].CPULimit.Add(cpu)
						}
						if memory, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
							metrics[podKey].MemoryLimit.Add(memory)
						}
					}
				}

				// Calculate utilization percentages
				if !metrics[podKey].CPURequest.IsZero() {
					metrics[podKey].CPUUtilization = float64(metrics[podKey].ActualCPU.MilliValue()) / float64(metrics[podKey].CPURequest.MilliValue()) * 100
				}
				if !metrics[podKey].MemoryRequest.IsZero() {
					metrics[podKey].MemoryUtilization = float64(metrics[podKey].ActualMemory.Value()) / float64(metrics[podKey].MemoryRequest.Value()) * 100
				}

				break
			}
		}
	}

	return metrics, nil
}

// analyzeNodeUtilization analyzes node utilization and generates recommendations
func (r *RightsizingRecommender) analyzeNodeUtilization(nodeName string, capacity, utilization *ResourceUtilization, currentCost float64) *RightsizingRecommendation {
	// Check if node is underutilized
	cpuWaste := 100 - utilization.CPUUtilization
	memoryWaste := 100 - utilization.MemoryUtilization

	if cpuWaste < r.thresholds.CPUWaste && memoryWaste < r.thresholds.MemoryWaste {
		return nil // Node is properly utilized
	}

	// Determine recommended size
	recommendedSize := r.recommendNodeSize(capacity, utilization)
	if recommendedSize == "" {
		return nil
	}

	// Estimate savings
	recommendedCost := r.estimateNodeCost(recommendedSize)
	savings := currentCost - recommendedCost

	if savings <= 0 {
		return nil // No savings
	}

	// Calculate confidence
	confidence := r.calculateRecommendationConfidence(utilization)

	if confidence < r.thresholds.MinConfidence {
		return nil // Confidence too low
	}

	recommendation := &RightsizingRecommendation{
		ResourceType:             "node",
		ResourceName:             nodeName,
		CurrentConfiguration:     fmt.Sprintf("CPU: %s, Memory: %s", capacity.CPURequest.String(), capacity.MemoryRequest.String()),
		RecommendedConfiguration: recommendedSize,
		CurrentMonthlyCost:       currentCost,
		RecommendedMonthlyCost:   recommendedCost,
		PotentialSavings:         savings,
		Confidence:               confidence,
		Reason:                   r.generateRecommendationReason(cpuWaste, memoryWaste),
		Impact:                   r.assessImpact(utilization),
		Priority:                 r.calculatePriority(savings, confidence),
	}

	return recommendation
}

// analyzePodUtilization analyzes pod utilization and generates recommendations
func (r *RightsizingRecommender) analyzePodUtilization(podKey string, utilization *ResourceUtilization) *RightsizingRecommendation {
	// Check if pod is over/under-provisioned
	cpuOverprovisioned := utilization.CPURequest.MilliValue() > utilization.ActualCPU.MilliValue()*2
	memoryOverprovisioned := utilization.MemoryRequest.Value() > utilization.ActualMemory.Value()*2

	if !cpuOverprovisioned && !memoryOverprovisioned {
		return nil // Pod is properly sized
	}

	// Recommend new resource specifications
	recommendedCPU := r.recommendCPU(utilization)
	recommendedMemory := r.recommendMemory(utilization)

	// Estimate savings (simplified)
	currentCost := r.estimatePodCost(&utilization.CPURequest, &utilization.MemoryRequest)
	recommendedCost := r.estimatePodCost(recommendedCPU, recommendedMemory)
	savings := currentCost - recommendedCost

	if savings <= 0 {
		return nil
	}

	confidence := r.calculateRecommendationConfidence(utilization)

	if confidence < r.thresholds.MinConfidence {
		return nil
	}

	recommendation := &RightsizingRecommendation{
		ResourceType:             "pod",
		ResourceName:             podKey,
		CurrentConfiguration:     fmt.Sprintf("CPU: %s, Memory: %s", utilization.CPURequest.String(), utilization.MemoryRequest.String()),
		RecommendedConfiguration: fmt.Sprintf("CPU: %s, Memory: %s", recommendedCPU.String(), recommendedMemory.String()),
		CurrentMonthlyCost:       currentCost,
		RecommendedMonthlyCost:   recommendedCost,
		PotentialSavings:         savings,
		Confidence:               confidence,
		Reason:                   "Pod is over-provisioned",
		Impact:                   "Low - only affects pod resource allocation",
		Priority:                 r.calculatePriority(savings, confidence),
	}

	return recommendation
}

// analyzeDeploymentUtilization analyzes deployment utilization
func (r *RightsizingRecommender) analyzeDeploymentUtilization(deploymentName, namespace string, metrics *ResourceUtilization) *RightsizingRecommendation {
	// Similar logic to pod analysis but aggregated across deploymeappsv1 "k8s.io/api/apps/v1"nt
	recommendation := r.analyzePodUtilization(fmt.Sprintf("%s/%s", namespace, deploymentName), metrics)
	if recommendation != nil {
		recommendation.ResourceType = "deployment"
		recommendation.Impact = "Medium - affects all deployment replicas"
	}
	return recommendation
}

// aggregateDeploymentMetrics aggregates metrics for all pods in a deployment
func (r *RightsizingRecommender) aggregateDeploymentMetrics(deploymentName, namespace string) *ResourceUtilization {
	pods, err := r.k8sClient.CoreV1().Pods(namespace).List(r.ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", deploymentName),
	})
	if err != nil {
		return nil
	}

	aggregated := &ResourceUtilization{
		CPURequest:    *resource.NewMilliQuantity(0, resource.DecimalSI),
		MemoryRequest: *resource.NewQuantity(0, resource.BinarySI),
		ActualCPU:     *resource.NewMilliQuantity(0, resource.DecimalSI),
		ActualMemory:  *resource.NewQuantity(0, resource.BinarySI),
	}

	for _, pod := range pods.Items {
		podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		if podMetrics, ok := r.metrics[podKey]; ok {
			aggregated.CPURequest.Add(podMetrics.CPURequest)
			aggregated.MemoryRequest.Add(podMetrics.MemoryRequest)
			aggregated.ActualCPU.Add(podMetrics.ActualCPU)
			aggregated.ActualMemory.Add(podMetrics.ActualMemory)
			aggregated.PodCount++
		}
	}

	// Calculate average utilization
	if aggregated.PodCount > 0 {
		aggregated.CPUUtilization = float64(aggregated.ActualCPU.MilliValue()) / float64(aggregated.CPURequest.MilliValue()) * 100
		aggregated.MemoryUtilization = float64(aggregated.ActualMemory.Value()) / float64(aggregated.MemoryRequest.Value()) * 100
	}

	return aggregated
}

// recommendNodeSize recommends an appropriate node size
func (r *RightsizingRecommender) recommendNodeSize(capacity, utilization *ResourceUtilization) string {
	// Add 20% buffer to peak utilization
	recommendedCPU := float64(utilization.ActualCPU.MilliValue()) * 1.2
	recommendedMemory := float64(utilization.ActualMemory.Value()) * 1.2

	// Find smallest instance type that meets requirements
	// This would be integrated with the cost provider to get actual instance types
	return fmt.Sprintf("cpu: %.0fm, memory: %.0fMi", recommendedCPU, recommendedMemory/1024/1024)
}

// recommendCPU recommends appropriate CPU allocation
func (r *RightsizingRecommender) recommendCPU(utilization *ResourceUtilization) *resource.Quantity {
	// Use peak usage + 10% buffer
	recommendedMilliCPU := int64(float64(utilization.ActualCPU.MilliValue()) * 1.1)
	return resource.NewMilliQuantity(recommendedMilliCPU, resource.DecimalSI)
}

// recommendMemory recommends appropriate memory allocation
func (r *RightsizingRecommender) recommendMemory(utilization *ResourceUtilization) *resource.Quantity {
	// Use peak usage + 10% buffer
	recommendedBytes := int64(float64(utilization.ActualMemory.Value()) * 1.1)
	return resource.NewQuantity(recommendedBytes, resource.BinarySI)
}

// calculateRecommendationConfidence calculates confidence score for a recommendation
func (r *RightsizingRecommender) calculateRecommendationConfidence(utilization *ResourceUtilization) float64 {
	// Confidence based on data consistency and observation period
	// This would be more sophisticated in production, considering metric variance
	confidence := 0.8

	// Reduce confidence if utilization is very inconsistent
	if utilization.CPUUtilization < 10 || utilization.MemoryUtilization < 10 {
		confidence *= 0.8
	}

	return confidence
}

// generateRecommendationReason generates a human-readable reason
func (r *RightsizingRecommender) generateRecommendationReason(cpuWaste, memoryWaste float64) string {
	var reasons []string

	if cpuWaste > r.thresholds.CPUWaste {
		reasons = append(reasons, fmt.Sprintf("CPU is %.1f%% underutilized", cpuWaste))
	}
	if memoryWaste > r.thresholds.MemoryWaste {
		reasons = append(reasons, fmt.Sprintf("Memory is %.1f%% underutilized", memoryWaste))
	}

	return strings.Join(reasons, " and ")
}

// assessImpact assesses the impact of implementing the recommendation
func (r *RightsizingRecommender) assessImpact(utilization *ResourceUtilization) string {
	// Assess impact based on current utilization and resource type
	if utilization.CPUUtilization > 70 || utilization.MemoryUtilization > 70 {
		return "High - requires careful planning to avoid performance issues"
	}
	return "Low - minimal performance impact expected"
}

// calculatePriority calculates priority for a recommendation
func (r *RightsizingRecommender) calculatePriority(savings, confidence float64) int {
	// Simple priority calculation based on savings and confidence
	priorityScore := (savings / 100) * confidence

	if priorityScore > 10 {
		return 5 // Highest priority
	} else if priorityScore > 5 {
		return 4
	} else if priorityScore > 2 {
		return 3
	} else if priorityScore > 1 {
		return 2
	}
	return 1
}

// estimateNodeCost estimates cost for a node configuration
func (r *RightsizingRecommender) estimateNodeCost(nodeConfig string) float64 {
	// This would integrate with the cost provider to get actual costs
	// Simplified estimation for demonstration
	return 50.0 // $50/month placeholder
}

// estimatePodCost estimates cost for pod resources
func (r *RightsizingRecommender) estimatePodCost(cpu, memory *resource.Quantity) float64 {
	// Simple cost estimation based on resource allocation
	cpuCostPerMilliCore := 0.00001 // $0.00001 per milli-core-hour
	memoryCostPerMB := 0.0000005   // $0.0000005 per MB-hour

	hourlyPodCost := (float64(cpu.MilliValue()) * cpuCostPerMilliCore) +
		(float64(memory.Value()/(1024*1024)) * memoryCostPerMB)

	return hourlyPodCost * 24 * 30 // Monthly cost
}

// getNodeCapacity extracts node capacity from node map
func (r *RightsizingRecommender) getNodeCapacity(nodeMap map[string]interface{}) *ResourceUtilization {
	status, ok := nodeMap["status"].(map[string]interface{})
	if !ok {
		return nil
	}

	capacity, ok := status["capacity"].(map[string]interface{})
	if !ok {
		return nil
	}

	return &ResourceUtilization{
		CPURequest:    *parseQuantity(capacity["cpu"]),
		MemoryRequest: *parseQuantity(capacity["memory"]),
	}
}

// parseQuantity safely parses a quantity string
func parseQuantity(value interface{}) *resource.Quantity {
	if str, ok := value.(string); ok {
		q, err := resource.ParseQuantity(str)
		if err == nil {
			return &q
		}
	}
	return resource.NewQuantity(0, resource.DecimalSI)
}
