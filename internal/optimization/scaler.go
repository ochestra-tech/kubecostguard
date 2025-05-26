// File: internal/optimization/scaler.go
package optimization

import (
	"context"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	"log"
	"math"
	"sort"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/config"
	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScalingRecommendation represents a recommendation to scale a resource
type ScalingRecommendation struct {
	ResourceType        string
	ResourceName        string
	Namespace           string
	CurrentReplicas     int32
	RecommendedReplicas int32
	CurrentCost         float64
	RecommendedCost     float64
	PotentialSavings    float64
	Confidence          float64
	Reason              string
	Metrics             map[string]interface{}
	Priority            int
	Actions             []ScalingAction
}

// GenerateRecommendations generates scaling recommendations
func (s *Scaler) GenerateRecommendations(costData map[string]float64) ([]ScalingRecommendation, error) {
	ctx := context.Background()
	s.ctx = ctx

	var recommendations []ScalingRecommendation

	// Get all deployments for scaling recommendations
	deployments, err := s.k8sClient.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}

	for _, deployment := range deployments.Items {
		recommendation := s.analyzeDeploymentScaling(deployment, costData)
		if recommendation != nil {
			recommendations = append(recommendations, *recommendation)
		}
	}

	// Get all StatefulSets
	statefulSets, err := s.k8sClient.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to list StatefulSets: %v", err)
	} else {
		for _, statefulSet := range statefulSets.Items {
			recommendation := s.analyzeStatefulSetScaling(statefulSet, costData)
			if recommendation != nil {
				recommendations = append(recommendations, *recommendation)
			}
		}
	}

	// Check for custom resources with horizontal scaling support
	s.checkCustomResources(&recommendations)

	// Sort by potential savings
	sort.Slice(recommendations, func(i, j int) bool {
		return recommendations[i].PotentialSavings > recommendations[j].PotentialSavings
	})

	return recommendations, nil
}

// analyzeDeploymentScaling analyzes deployment for scaling opportunities
func (s *Scaler) analyzeDeploymentScaling(deployment appsv1.Deployment, costData map[string]float64) *ScalingRecommendation {
	// Get metrics for the deployment
	metrics, err := s.getDeploymentMetrics(deployment)
	if err != nil {
		log.Printf("Failed to get metrics for deployment %s: %v", deployment.Name, err)
		return nil
	}

	// Calculate current cost
	currentCost := s.calculateDeploymentCost(deployment, costData)

	// Determine optimal replica count
	optimalReplicas := s.calculateOptimalReplicas(metrics, *deployment.Spec.Replicas)

	if optimalReplicas == *deployment.Spec.Replicas {
		return nil // No scaling needed
	}

	// Calculate recommended cost
	recommendedCost := currentCost * float64(optimalReplicas) / float64(*deployment.Spec.Replicas)
	savings := currentCost - recommendedCost

	// Create recommendation
	return &ScalingRecommendation{
		ResourceType:        "Deployment",
		ResourceName:        deployment.Name,
		Namespace:           deployment.Namespace,
		CurrentReplicas:     *deployment.Spec.Replicas,
		RecommendedReplicas: optimalReplicas,
		CurrentCost:         currentCost,
		RecommendedCost:     recommendedCost,
		PotentialSavings:    savings,
		Confidence:          s.calculateConfidence(metrics),
		Reason:              s.generateScalingReason(metrics, *deployment.Spec.Replicas, optimalReplicas),
		Metrics:             s.convertMetricsToMap(metrics),
		Priority:            s.calculatePriority(savings, metrics),
		Actions:             s.generateScalingActions(deployment, optimalReplicas),
	}
}

// calculateOptimalReplicas calculates the optimal number of replicas
func (s *Scaler) calculateOptimalReplicas(metrics *ScalingMetrics, currentReplicas int32) int32 {
	// Use both CPU and memory metrics to determine scaling
	cpuBasedReplicas := s.calculateReplicasBasedOnCPU(metrics, currentReplicas)
	memoryBasedReplicas := s.calculateReplicasBasedOnMemory(metrics, currentReplicas)

	// Take the maximum to ensure we don't under-provision
	optimalReplicas := int32(math.Max(float64(cpuBasedReplicas), float64(memoryBasedReplicas)))

	// Apply minimum and maximum constraints
	minReplicas := int32(1)
	maxReplicas := int32(10) // Default maximum

	if optimalReplicas < minReplicas {
		optimalReplicas = minReplicas
	}
	if optimalReplicas > maxReplicas {
		optimalReplicas = maxReplicas
	}

	return optimalReplicas
}

// calculateReplicasBasedOnCPU calculates replicas based on CPU utilization
func (s *Scaler) calculateReplicasBasedOnCPU(metrics *ScalingMetrics, currentReplicas int32) int32 {
	targetUtilization := 70.0 // Target 70% CPU utilization

	if metrics.CPUUtilization > 0 {
		requiredReplicas := float64(currentReplicas) * (metrics.CPUUtilization / targetUtilization)
		return int32(math.Ceil(requiredReplicas))
	}

	return currentReplicas
}

// calculateReplicasBasedOnMemory calculates replicas based on memory utilization
func (s *Scaler) calculateReplicasBasedOnMemory(metrics *ScalingMetrics, currentReplicas int32) int32 {
	targetUtilization := 75.0 // Target 75% memory utilization

	if metrics.MemoryUtilization > 0 {
		requiredReplicas := float64(currentReplicas) * (metrics.MemoryUtilization / targetUtilization)
		return int32(math.Ceil(requiredReplicas))
	}

	return currentReplicas
}

// calculateDeploymentCost calculates the cost of a deployment
func (s *Scaler) calculateDeploymentCost(deployment appsv1.Deployment, costData map[string]float64) float64 {
	// Get pods for this deployment
	pods, err := s.k8sClient.CoreV1().Pods(deployment.Namespace).List(s.ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(deployment.Spec.Selector),
	})
	if err != nil {
		return 0
	}

	totalCost := 0.0
	for _, pod := range pods.Items {
		podCost := s.calculatePodCost(pod)
		totalCost += podCost
	}

	return totalCost
}

// calculatePodCost calculates the cost of a pod
func (s *Scaler) calculatePodCost(pod corev1.Pod) float64 {
	// Simple cost estimation based on resource requests
	cpuCost := 0.0
	memoryCost := 0.0

	for _, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				cpuCost += s.estimateCPUCost(cpu)
			}
			if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				memoryCost += s.estimateMemoryCost(memory)
			}
		}
	}

	return cpuCost + memoryCost
}

// getDeploymentMetrics gets scaling metrics for a deployment
func (s *Scaler) getDeploymentMetrics(deployment appsv1.Deployment) (*ScalingMetrics, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)
	if s.cacheExpiry.After(time.Now()) {
		if cachedMetrics, ok := s.metricsCache[cacheKey]; ok {
			return cachedMetrics, nil
		}
	}

	// Get pods for this deployment
	pods, err := s.k8sClient.CoreV1().Pods(deployment.Namespace).List(s.ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(deployment.Spec.Selector),
	})
	if err != nil {
		return nil, err
	}

	metrics := &ScalingMetrics{
		MetricHistory: make([]MetricPoint, 0),
	}

	// Aggregate metrics from all pods
	totalCPU := float64(0)
	totalMemory := float64(0)
	podCount := 0

	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		podMetrics, err := s.getPodMetrics(pod)
		if err != nil {
			continue
		}

		totalCPU += podMetrics.CPUUtilization
		totalMemory += podMetrics.MemoryUtilization
		podCount++
	}

	if podCount > 0 {
		metrics.CPUUtilization = totalCPU / float64(podCount)
		metrics.MemoryUtilization = totalMemory / float64(podCount)
	}

	// Calculate peak and average utilization
	s.calculateUtilizationStats(metrics)

	// Cache the results
	s.metricsCache[cacheKey] = metrics
	s.cacheExpiry = time.Now().Add(s.cacheDuration)

	return metrics, nil
}

// getPodMetrics gets metrics for a specific pod
func (s *Scaler) getPodMetrics(pod corev1.Pod) (*ScalingMetrics, error) {
	// Get pod metrics from metrics server
	podMetrics, err := s.k8sClient.MetricsV1beta1().PodMetricses(pod.Namespace).Get(s.ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	metrics := &ScalingMetrics{}

	// Calculate CPU and memory utilization
	cpuUsage := int64(0)
	memoryUsage := int64(0)

	for _, container := range podMetrics.Containers {
		cpuUsage += container.Usage.Cpu().MilliValue()
		memoryUsage += container.Usage.Memory().Value()
	}

	// Get resource requests for comparison
	cpuRequest := int64(0)
	memoryRequest := int64(0)

	for _, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				cpuRequest += cpu.MilliValue()
			}
			if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				memoryRequest += memory.Value()
			}
		}
	}

	// Calculate utilization percentages
	if cpuRequest > 0 {
		metrics.CPUUtilization = float64(cpuUsage) / float64(cpuRequest) * 100
	}
	if memoryRequest > 0 {
		metrics.MemoryUtilization = float64(memoryUsage) / float64(memoryRequest) * 100
	}

	return metrics, nil
}

// generateScalingActions generates specific actions for scaling
func (s *Scaler) generateScalingActions(deployment appsv1.Deployment, targetReplicas int32) []ScalingAction {
	actions := make([]ScalingAction, 0)

	// Add replica scaling action
	actions = append(actions, ScalingAction{
		Type:        "scale",
		Target:      "replicas",
		Value:       targetReplicas,
		Description: fmt.Sprintf("Scale replicas from %d to %d", *deployment.Spec.Replicas, targetReplicas),
	})

	// Add HPA recommendation if scaling up significantly
	if targetReplicas > *deployment.Spec.Replicas*2 {
		actions = append(actions, ScalingAction{
			Type:   "create-hpa",
			Target: deployment.Name,
			Value: map[string]interface{}{
				"minReplicas":                    *deployment.Spec.Replicas,
				"maxReplicas":                    targetReplicas,
				"targetCPUUtilizationPercentage": 70,
			},
			Description: "Create HorizontalPodAutoscaler for dynamic scaling",
		})
	}

	return actions
}

// ApplyRecommendations applies scaling recommendations
func (s *Scaler) ApplyRecommendations(recommendations []ScalingRecommendation) (int, error) {
	applied := 0

	for _, rec := range recommendations {
		if rec.Confidence < 0.7 {
			continue // Skip low confidence recommendations
		}

		err := s.applyScalingRecommendation(rec)
		if err != nil {
			log.Printf("Failed to apply recommendation for %s: %v", rec.ResourceName, err)
			continue
		}

		applied++
	}

	return applied, nil
}

// applyScalingRecommendation applies a single scaling recommendation
func (s *Scaler) applyScalingRecommendation(rec ScalingRecommendation) error {
	switch rec.ResourceType {
	case "Deployment":
		return s.scaleDeployment(rec)
	case "StatefulSet":
		return s.scaleStatefulSet(rec)
	default:
		return fmt.Errorf("unsupported resource type: %s", rec.ResourceType)
	}
}

// scaleDeployment scales a deployment to the recommended replicas
func (s *Scaler) scaleDeployment(rec ScalingRecommendation) error {
	deployment, err := s.k8sClient.AppsV1().Deployments(rec.Namespace).Get(s.ctx, rec.ResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Update replica count
	deployment.Spec.Replicas = &rec.RecommendedReplicas

	_, err = s.k8sClient.AppsV1().Deployments(rec.Namespace).Update(s.ctx, deployment, metav1.UpdateOptions{})
	return err
}

// calculateConfidence calculates confidence score for a recommendation
func (s *Scaler) calculateConfidence(metrics *ScalingMetrics) float64 {
	confidence := 1.0

	// Reduce confidence if utilization data is missing
	if metrics.CPUUtilization == 0 {
		confidence *= 0.5
	}
	if metrics.MemoryUtilization == 0 {
		confidence *= 0.5
	}

	// Reduce confidence if utilization is highly variable
	if len(metrics.MetricHistory) > 1 {
		variance := s.calculateVariance(metrics.MetricHistory)
		if variance > 30.0 { // High variance
			confidence *= 0.7
		}
	}

	return confidence
}

// generateScalingReason generates a human-readable reason for scaling
func (s *Scaler) generateScalingReason(metrics *ScalingMetrics, current, recommended int32) string {
	if recommended > current {
		return fmt.Sprintf("Scale up from %d to %d replicas due to high resource utilization (CPU: %.1f%%, Memory: %.1f%%)",
			current, recommended, metrics.CPUUtilization, metrics.MemoryUtilization)
	} else {
		return fmt.Sprintf("Scale down from %d to %d replicas due to low resource utilization (CPU: %.1f%%, Memory: %.1f%%)",
			current, recommended, metrics.CPUUtilization, metrics.MemoryUtilization)
	}
}

// convertMetricsToMap converts ScalingMetrics to a map for JSON serialization
func (s *Scaler) convertMetricsToMap(metrics *ScalingMetrics) map[string]interface{} {
	return map[string]interface{}{
		"cpu_utilization":     metrics.CPUUtilization,
		"memory_utilization":  metrics.MemoryUtilization,
		"request_rate":        metrics.RequestRate,
		"response_time":       metrics.ResponseTime,
		"peak_utilization":    metrics.PeakUtilization,
		"average_utilization": metrics.AverageUtilization,
	}
}

// calculatePriority calculates priority for a scaling recommendation
func (s *Scaler) calculatePriority(savings float64, metrics *ScalingMetrics) int {
	priority := 3 // Default priority

	// Increase priority for high utilization
	if metrics.CPUUtilization > 85 || metrics.MemoryUtilization > 85 {
		priority = 5
	} else if metrics.CPUUtilization > 50 || metrics.MemoryUtilization > 50 {
		priority = 4
	}

	// Increase priority for large cost savings
	if savings > 100 {
		priority = 5
	} else if savings > 50 {
		priority = 4
	}

	// Decrease priority for very low savings
	if savings < 10 {
		priority = 1
	}

	return priority
}

// calculateVariance calculates the variance of metric values
func (s *Scaler) calculateVariance(metrics []MetricPoint) float64 {
	if len(metrics) < 2 {
		return 0
	}

	// Calculate mean
	sum := 0.0
	for _, point := range metrics {
		sum += point.Value
	}
	mean := sum / float64(len(metrics))

	// Calculate variance
	sumSquaredDiff := 0.0
	for _, point := range metrics {
		diff := point.Value - mean
		sumSquaredDiff += diff * diff
	}

	return sumSquaredDiff / float64(len(metrics))
}

// estimateCPUCost estimates the cost for CPU resources
func (s *Scaler) estimateCPUCost(cpu resource.Quantity) float64 {
	// Cost per milli-core-hour (placeholder value)
	costPerMilliCore := 0.00002 // $0.00002 per milli-core-hour
	milliCores := float64(cpu.MilliValue())

	// Return monthly cost
	return milliCores * costPerMilliCore * 24 * 30
}

// estimateMemoryCost estimates the cost for memory resources
func (s *Scaler) estimateMemoryCost(memory resource.Quantity) float64 {
	// Cost per MB-hour (placeholder value)
	costPerMB := 0.000001 // $0.000001 per MB-hour
	megaBytes := float64(memory.Value()) / (1024 * 1024)

	// Return monthly cost
	return megaBytes * costPerMB * 24 * 30
}

// calculateUtilizationStats calculates various utilization statistics
func (s *Scaler) calculateUtilizationStats(metrics *ScalingMetrics) {
	if len(metrics.MetricHistory) == 0 {
		return
	}

	// Calculate peak utilization
	peakCPU := 0.0
	sumCPU := 0.0

	for _, point := range metrics.MetricHistory {
		if point.Value > peakCPU {
			peakCPU = point.Value
		}
		sumCPU += point.Value
	}

	metrics.PeakUtilization = peakCPU
	metrics.AverageUtilization = sumCPU / float64(len(metrics.MetricHistory))
}

// checkCustomResources checks for custom resources that support horizontal scaling
func (s *Scaler) checkCustomResources(recommendations *[]ScalingRecommendation) {
	// This would check for custom resources that support HPA
	// For example, checking for CRDs with scale subresource enabled

	// Get discovery client to check for custom resources
	discoveryClient := s.k8sClient.Discovery()

	// Get API resources
	resourceLists, err := discoveryClient.ServerResources()
	if err != nil {
		log.Printf("Failed to get server resources: %v", err)
		return
	}

	// Look for resources with scale subresource
	for _, resourceList := range resourceLists {
		for _, resource := range resourceList.APIResources {
			// Check if resource has scale subresource
			hasScale := false
			for _, verb := range resource.Verbs {
				if verb == "scale" {
					hasScale = true
					break
				}
			}

			if hasScale && !s.isKnownScalableResource(resource.Kind) {
				// Query for instances of this custom resource
				s.checkCustomResourceInstances(resource, recommendations)
			}
		}
	}
}

// isKnownScalableResource checks if a resource type is already handled
func (s *Scaler) isKnownScalableResource(kind string) bool {
	knownTypes := []string{
		"Deployment",
		"ReplicaSet",
		"StatefulSet",
		"ReplicationController",
	}

	for _, knownType := range knownTypes {
		if knownType == kind {
			return true
		}
	}

	return false
}

// checkCustomResourceInstances checks instances of a custom resource for scaling opportunities
func (s *Scaler) checkCustomResourceInstances(resource metav1.APIResource, recommendations *[]ScalingRecommendation) {
	// This would query for instances of the custom resource and analyze them
	// Implementation depends on the specific CRD structure
	// For now, just logging that we found a scalable custom resource
	log.Printf("Found scalable custom resource: %s", resource.Kind)
}

// analyzeStatefulSetScaling analyzes StatefulSet for scaling opportunities
func (s *Scaler) analyzeStatefulSetScaling(statefulSet appsv1.StatefulSet, costData map[string]float64) *ScalingRecommendation {
	// Similar to deployment analysis but with StatefulSet-specific considerations
	metrics, err := s.getStatefulSetMetrics(statefulSet)
	if err != nil {
		log.Printf("Failed to get metrics for StatefulSet %s: %v", statefulSet.Name, err)
		return nil
	}

	currentCost := s.calculateStatefulSetCost(statefulSet, costData)
	optimalReplicas := s.calculateOptimalReplicas(metrics, *statefulSet.Spec.Replicas)

	if optimalReplicas == *statefulSet.Spec.Replicas {
		return nil
	}

	recommendedCost := currentCost * float64(optimalReplicas) / float64(*statefulSet.Spec.Replicas)
	savings := currentCost - recommendedCost

	return &ScalingRecommendation{
		ResourceType:        "StatefulSet",
		ResourceName:        statefulSet.Name,
		Namespace:           statefulSet.Namespace,
		CurrentReplicas:     *statefulSet.Spec.Replicas,
		RecommendedReplicas: optimalReplicas,
		CurrentCost:         currentCost,
		RecommendedCost:     recommendedCost,
		PotentialSavings:    savings,
		Confidence:          s.calculateConfidence(metrics),
		Reason:              s.generateScalingReason(metrics, *statefulSet.Spec.Replicas, optimalReplicas),
		Metrics:             s.convertMetricsToMap(metrics),
		Priority:            s.calculatePriority(savings, metrics),
		Actions:             s.generateStatefulSetScalingActions(statefulSet, optimalReplicas),
	}
}

// getStatefulSetMetrics gets scaling metrics for a StatefulSet
func (s *Scaler) getStatefulSetMetrics(statefulSet appsv1.StatefulSet) (*ScalingMetrics, error) {
	// Similar to getDeploymentMetrics but for StatefulSet
	// This would aggregate metrics from all pods managed by the StatefulSet
	cacheKey := fmt.Sprintf("statefulset/%s/%s", statefulSet.Namespace, statefulSet.Name)
	if s.cacheExpiry.After(time.Now()) {
		if cachedMetrics, ok := s.metricsCache[cacheKey]; ok {
			return cachedMetrics, nil
		}
	}

	// Get pods for this StatefulSet
	pods, err := s.k8sClient.CoreV1().Pods(statefulSet.Namespace).List(s.ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(statefulSet.Spec.Selector),
	})
	if err != nil {
		return nil, err
	}

	metrics := &ScalingMetrics{
		MetricHistory: make([]MetricPoint, 0),
	}

	// Aggregate metrics from all pods
	totalCPU := float64(0)
	totalMemory := float64(0)
	podCount := 0

	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		podMetrics, err := s.getPodMetrics(pod)
		if err != nil {
			continue
		}

		totalCPU += podMetrics.CPUUtilization
		totalMemory += podMetrics.MemoryUtilization
		podCount++
	}

	if podCount > 0 {
		metrics.CPUUtilization = totalCPU / float64(podCount)
		metrics.MemoryUtilization = totalMemory / float64(podCount)
	}

	// Calculate peak and average utilization
	s.calculateUtilizationStats(metrics)

	// Cache the results
	s.metricsCache[cacheKey] = metrics
	s.cacheExpiry = time.Now().Add(s.cacheDuration)

	return metrics, nil
}

// calculateStatefulSetCost calculates the cost of a StatefulSet
func (s *Scaler) calculateStatefulSetCost(statefulSet appsv1.StatefulSet, costData map[string]float64) float64 {
	// Similar to calculateDeploymentCost but for StatefulSet
	pods, err := s.k8sClient.CoreV1().Pods(statefulSet.Namespace).List(s.ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(statefulSet.Spec.Selector),
	})
	if err != nil {
		return 0
	}

	totalCost := 0.0
	for _, pod := range pods.Items {
		podCost := s.calculatePodCost(pod)
		totalCost += podCost
	}

	return totalCost
}

// generateStatefulSetScalingActions generates scaling actions for StatefulSet
func (s *Scaler) generateStatefulSetScalingActions(statefulSet appsv1.StatefulSet, targetReplicas int32) []ScalingAction {
	actions := make([]ScalingAction, 0)

	// StatefulSet scaling needs to be done carefully
	actions = append(actions, ScalingAction{
		Type:   "scale-statefulset",
		Target: "replicas",
		Value:  targetReplicas,
		Description: fmt.Sprintf("Scale StatefulSet from %d to %d replicas (ordered scaling)",
			*statefulSet.Spec.Replicas, targetReplicas),
	})

	// Add volume template check for scaling down
	if targetReplicas < *statefulSet.Spec.Replicas {
		actions = append(actions, ScalingAction{
			Type:        "check-volumes",
			Target:      statefulSet.Name,
			Value:       "orphaned-pvcs",
			Description: "Check for orphaned PVCs that may be left after scaling down",
		})
	}

	return actions
}

// scaleStatefulSet scales a StatefulSet to the recommended replicas
func (s *Scaler) scaleStatefulSet(rec ScalingRecommendation) error {
	statefulSet, err := s.k8sClient.AppsV1().StatefulSets(rec.Namespace).Get(s.ctx, rec.ResourceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Update replica count
	statefulSet.Spec.Replicas = &rec.RecommendedReplicas

	_, err = s.k8sClient.AppsV1().StatefulSets(rec.Namespace).Update(s.ctx, statefulSet, metav1.UpdateOptions{})
	return err
}

// ScalingAction represents a specific scaling action
type ScalingAction struct {
	Type        string
	Target      string
	Value       interface{}
	Description string
}

// ScalingMetrics contains metrics needed for scaling decisions
type ScalingMetrics struct {
	CPUUtilization     float64
	MemoryUtilization  float64
	RequestRate        float64
	ResponseTime       float64
	CustomMetrics      map[string]float64
	PeakUtilization    float64
	AverageUtilization float64
	MetricHistory      []MetricPoint
}

// MetricPoint represents a metric at a specific time
type MetricPoint struct {
	Timestamp time.Time
	Value     float64
}

// Scaler handles resource scaling recommendations
type Scaler struct {
	k8sClient     *kubernetes.Client
	config        config.OptimizationConfig
	ctx           context.Context
	metricsCache  map[string]*ScalingMetrics
	cacheExpiry   time.Time
	cacheDuration time.Duration
}

// NewScaler creates a new scaler
func NewScaler(k8sClient *kubernetes.Client, config config.OptimizationConfig) *Scaler {
	return &Scaler{
		k8sClient:     k8sClient,
		config:        config,
		metricsCache:  make(map[string]*ScalingMetrics),
		cacheExpiry:   time.Now(),
		cacheDuration: 5 * time.Minute,
	}
}
