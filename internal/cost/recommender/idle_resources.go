// File: internal/cost/recommender/idle_resources.go
package recommender

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IdleResourceType represents different types of idle resources
type IdleResourceType string

const (
	IdleNode         IdleResourceType = "idle-node"
	IdlePod          IdleResourceType = "idle-pod"
	IdleDeployment   IdleResourceType = "idle-deployment"
	IdleNamespace    IdleResourceType = "idle-namespace"
	IdleVolume       IdleResourceType = "idle-volume"
	IdleLoadBalancer IdleResourceType = "idle-load-balancer"
)

// IdleResourceStatus represents the status of an idle resource
type IdleResourceStatus string

const (
	StatusIdle          IdleResourceStatus = "idle"
	StatusUnderutilized IdleResourceStatus = "underutilized"
	StatusTerminated    IdleResourceStatus = "terminated"
	StatusReclaimable   IdleResourceStatus = "reclaimable"
)

// IdleResourceRecommendation represents a recommendation to optimize idle resources
type IdleResourceRecommendation struct {
	Type                 IdleResourceType
	ResourceName         string
	ResourceNamespace    string
	Status               IdleResourceStatus
	IdleDuration         time.Duration
	LastActiveTime       time.Time
	CurrentCost          float64
	PotentialSavings     float64
	RecommendedAction    string
	Priority             int // 1-5, 5 being highest
	Confidence           float64
	Impact               string
	ReclaimableResources *ResourceAllocation
	Details              map[string]interface{}
}

// ResourceAllocation represents allocatable resources
type ResourceAllocation struct {
	CPU     resource.Quantity
	Memory  resource.Quantity
	Pods    int
	Storage resource.Quantity
}

// IdleResourceRecommender identifies and recommends optimization for idle resources
type IdleResourceRecommender struct {
	k8sClient       *kubernetes.Client
	ctx             context.Context
	recommendations []*IdleResourceRecommendation
	metricsHistory  map[string][]ResourceUtilization
	thresholds      struct {
		NodeIdlePercent          float64       // CPU/Memory utilization below this is idle
		PodIdleDuration          time.Duration // How long a pod must be idle
		DeploymentIdleDuration   time.Duration
		NamespaceIdleDuration    time.Duration
		VolumeUnusedDuration     time.Duration
		LoadBalancerIdleDuration time.Duration
		MetricsRetention         time.Duration // How long to keep metrics history
	}
}

// NewIdleResourceRecommender creates a new idle resource recommender
func NewIdleResourceRecommender(k8sClient *kubernetes.Client) *IdleResourceRecommender {
	return &IdleResourceRecommender{
		k8sClient:       k8sClient,
		recommendations: make([]*IdleResourceRecommendation, 0),
		metricsHistory:  make(map[string][]ResourceUtilization),
		thresholds: struct {
			NodeIdlePercent          float64
			PodIdleDuration          time.Duration
			DeploymentIdleDuration   time.Duration
			NamespaceIdleDuration    time.Duration
			VolumeUnusedDuration     time.Duration
			LoadBalancerIdleDuration time.Duration
			MetricsRetention         time.Duration
		}{
			NodeIdlePercent:          5.0,                 // Less than 5% CPU/Memory utilization
			PodIdleDuration:          7 * 24 * time.Hour,  // 7 days
			DeploymentIdleDuration:   14 * 24 * time.Hour, // 14 days
			NamespaceIdleDuration:    30 * 24 * time.Hour, // 30 days
			VolumeUnusedDuration:     30 * 24 * time.Hour, // 30 days
			LoadBalancerIdleDuration: 7 * 24 * time.Hour,  // 7 days
			MetricsRetention:         90 * 24 * time.Hour, // 90 days
		},
	}
}

// GenerateRecommendations generates recommendations for idle resources
func (r *IdleResourceRecommender) GenerateRecommendations(resources map[string]interface{}, costs map[string]float64) (interface{}, error) {
	ctx := context.Background()
	r.ctx = ctx

	// Clear previous recommendations
	r.recommendations = make([]*IdleResourceRecommendation, 0)

	// Check for idle nodes
	if err := r.identifyIdleNodes(resources, costs); err != nil {
		log.Printf("Error identifying idle nodes: %v", err)
	}

	// Check for idle pods
	if err := r.identifyIdlePods(); err != nil {
		log.Printf("Error identifying idle pods: %v", err)
	}

	// Check for idle deployments
	if err := r.identifyIdleDeployments(); err != nil {
		log.Printf("Error identifying idle deployments: %v", err)
	}

	// Check for idle namespaces
	if err := r.identifyIdleNamespaces(); err != nil {
		log.Printf("Error identifying idle namespaces: %v", err)
	}

	// Check for unused volumes
	if err := r.identifyUnusedVolumes(); err != nil {
		log.Printf("Error identifying unused volumes: %v", err)
	}

	// Check for idle load balancers
	if err := r.identifyIdleLoadBalancers(); err != nil {
		log.Printf("Error identifying idle load balancers: %v", err)
	}

	// Sort recommendations by potential savings
	r.sortRecommendationsByPriority()

	return r.recommendations, nil
}

// Name returns the recommender name
func (r *IdleResourceRecommender) Name() string {
	return "idle-resource-recommender"
}

// identifyIdleNodes identifies nodes that are underutilized
func (r *IdleResourceRecommender) identifyIdleNodes(resources map[string]interface{}, costs map[string]float64) error {
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

		// Check if node is idle
		if metrics, ok := nodeMetrics[nodeName]; ok {
			if r.isNodeIdle(metrics) {
				recommendation := r.createIdleNodeRecommendation(nodeName, metrics, costs[nodeName])
				r.recommendations = append(r.recommendations, recommendation)
			}
		}
	}

	return nil
}

// identifyIdlePods identifies pods that are not running or inactive
func (r *IdleResourceRecommender) identifyIdlePods() error {
	pods, err := r.k8sClient.CoreV1().Pods("").List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	for _, pod := range pods.Items {
		idleDuration := r.getPodIdleDuration(pod)
		if idleDuration > r.thresholds.PodIdleDuration {
			recommendation := r.createIdlePodRecommendation(pod, idleDuration)
			r.recommendations = append(r.recommendations, recommendation)
		}
	}

	return nil
}

// identifyIdleDeployments identifies deployments with zero replicas or no activity
func (r *IdleResourceRecommender) identifyIdleDeployments() error {
	deployments, err := r.k8sClient.AppsV1().Deployments("").List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	for _, deployment := range deployments.Items {
		if r.isDeploymentIdle(deployment) {
			recommendation := r.createIdleDeploymentRecommendation(deployment)
			r.recommendations = append(r.recommendations, recommendation)
		}
	}

	return nil
}

// identifyIdleNamespaces identifies namespaces with no active resources
func (r *IdleResourceRecommender) identifyIdleNamespaces() error {
	namespaces, err := r.k8sClient.CoreV1().Namespaces().List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	for _, namespace := range namespaces.Items {
		idleDuration, isIdle := r.isNamespaceIdle(namespace)
		if isIdle {
			recommendation := r.createIdleNamespaceRecommendation(namespace, idleDuration)
			r.recommendations = append(r.recommendations, recommendation)
		}
	}

	return nil
}

// identifyUnusedVolumes identifies persistent volumes that are not mounted
func (r *IdleResourceRecommender) identifyUnusedVolumes() error {
	pvcs, err := r.k8sClient.CoreV1().PersistentVolumeClaims("").List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list PVCs: %w", err)
	}

	for _, pvc := range pvcs.Items {
		if r.isPVCUnused(pvc) {
			recommendation := r.createUnusedVolumeRecommendation(pvc)
			r.recommendations = append(r.recommendations, recommendation)
		}
	}

	return nil
}

// identifyIdleLoadBalancers identifies load balancers with no traffic
func (r *IdleResourceRecommender) identifyIdleLoadBalancers() error {
	services, err := r.k8sClient.CoreV1().Services("").List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, service := range services.Items {
		if service.Spec.Type == corev1.ServiceTypeLoadBalancer {
			if r.isLoadBalancerIdle(service) {
				recommendation := r.createIdleLoadBalancerRecommendation(service)
				r.recommendations = append(r.recommendations, recommendation)
			}
		}
	}

	return nil
}

// isNodeIdle checks if a node is idle based on utilization
func (r *IdleResourceRecommender) isNodeIdle(metrics *ResourceUtilization) bool {
	return metrics.CPUUtilization < r.thresholds.NodeIdlePercent &&
		metrics.MemoryUtilization < r.thresholds.NodeIdlePercent
}

// getPodIdleDuration calculates how long a pod has been idle
func (r *IdleResourceRecommender) getPodIdleDuration(pod corev1.Pod) time.Duration {
	// Check if pod is not running
	if pod.Status.Phase != corev1.PodRunning {
		if pod.Status.StartTime != nil {
			return time.Since(pod.Status.StartTime.Time)
		}
	}

	// Check if pod has any active containers
	allContainersTerminated := true
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Running != nil {
			allContainersTerminated = false
			break
		}
	}

	if allContainersTerminated {
		return time.Since(pod.CreationTimestamp.Time)
	}

	return 0
}

// isDeploymentIdle checks if a deployment is idle
func (r *IdleResourceRecommender) isDeploymentIdle(deployment appsv1.Deployment) bool {
	// Check if deployment has zero replicas
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		return true
	}

	// Check if all replicas are unavailable for extended period
	if deployment.Status.UnavailableReplicas == deployment.Status.Replicas {
		// Check for recent update
		for _, condition := range deployment.Status.Conditions {
			if condition.Type == appsv1.DeploymentProgressing {
				if time.Since(condition.LastUpdateTime.Time) > r.thresholds.DeploymentIdleDuration {
					return true
				}
			}
		}
	}

	return false
}

// isNamespaceIdle checks if a namespace has been idle
func (r *IdleResourceRecommender) isNamespaceIdle(namespace corev1.Namespace) (time.Duration, bool) {
	// Check if namespace has any active resources
	pods, err := r.k8sClient.CoreV1().Pods(namespace.Name).List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return 0, false
	}

	if len(pods.Items) == 0 {
		// Check when the last pod was deleted
		events, err := r.k8sClient.CoreV1().Events(namespace.Name).List(r.ctx, metav1.ListOptions{
			FieldSelector: "involvedObject.kind=Pod",
		})
		if err == nil && len(events.Items) > 0 {
			// Find the most recent event
			var mostRecentEvent corev1.Event
			for _, event := range events.Items {
				if mostRecentEvent.LastTimestamp.Time.Before(event.LastTimestamp.Time) {
					mostRecentEvent = event
				}
			}

			idleDuration := time.Since(mostRecentEvent.LastTimestamp.Time)
			if idleDuration > r.thresholds.NamespaceIdleDuration {
				return idleDuration, true
			}
		}
	}

	return 0, false
}

// isPVCUnused checks if a PVC is not mounted by any pod
func (r *IdleResourceRecommender) isPVCUnused(pvc corev1.PersistentVolumeClaim) bool {
	// Check if PVC is bound
	if pvc.Status.Phase != corev1.ClaimBound {
		return true
	}

	// Check if PVC is mounted by any pod
	pods, err := r.k8sClient.CoreV1().Pods(pvc.Namespace).List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return false
	}

	for _, pod := range pods.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == pvc.Name {
				return false
			}
		}
	}

	return true
}

// isLoadBalancerIdle checks if a load balancer has active connections
func (r *IdleResourceRecommender) isLoadBalancerIdle(service corev1.Service) bool {
	// In a real implementation, you would check load balancer metrics
	// For now, we check if there are any endpoints
	endpoints, err := r.k8sClient.CoreV1().Endpoints(service.Namespace).Get(r.ctx, service.Name, metav1.GetOptions{})
	if err != nil || endpoints == nil {
		return true
	}

	// Check if load balancer has any endpoints
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			return false
		}
	}

	return true
}

// createIdleNodeRecommendation creates a recommendation for an idle node
func (r *IdleResourceRecommender) createIdleNodeRecommendation(nodeName string, metrics *ResourceUtilization, cost float64) *IdleResourceRecommendation {
	// Estimate reclaimable resources
	reclaimable := &ResourceAllocation{
		CPU:    metrics.ActualCPU,
		Memory: metrics.ActualMemory,
	}

	return &IdleResourceRecommendation{
		Type:             IdleNode,
		ResourceName:     nodeName,
		Status:           StatusUnderutilized,
		CurrentCost:      cost,
		PotentialSavings: cost * 0.9, // Assume 90% of cost can be saved
		RecommendedAction: fmt.Sprintf("Migrate workloads and terminate node (CPU: %.1f%%, Memory: %.1f%%)",
			metrics.CPUUtilization, metrics.MemoryUtilization),
		Priority:             r.calculateIdlePriority(cost),
		Confidence:           0.85,
		Impact:               "High - requires workload migration",
		ReclaimableResources: reclaimable,
		Details: map[string]interface{}{
			"cpu_utilization":    metrics.CPUUtilization,
			"memory_utilization": metrics.MemoryUtilization,
			"node_cost":          cost,
		},
	}
}

// createIdlePodRecommendation creates a recommendation for an idle pod
func (r *IdleResourceRecommender) createIdlePodRecommendation(pod corev1.Pod, idleDuration time.Duration) *IdleResourceRecommendation {
	// Calculate potential savings based on resource requests
	requestedCPU := int64(0)
	requestedMemory := int64(0)

	for _, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				requestedCPU += cpu.MilliValue()
			}
			if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				requestedMemory += memory.Value()
			}
		}
	}

	estimatedMonthlyCost := r.estimatePodCost(requestedCPU, requestedMemory)

	return &IdleResourceRecommendation{
		Type:              IdlePod,
		ResourceName:      pod.Name,
		ResourceNamespace: pod.Namespace,
		Status:            StatusIdle,
		IdleDuration:      idleDuration,
		LastActiveTime:    pod.CreationTimestamp.Time,
		CurrentCost:       estimatedMonthlyCost,
		PotentialSavings:  estimatedMonthlyCost,
		RecommendedAction: "Terminate idle pod",
		Priority:          3,
		Confidence:        0.9,
		Impact:            "Low - no active workload affected",
		Details: map[string]interface{}{
			"idle_duration":    idleDuration.String(),
			"pod_phase":        pod.Status.Phase,
			"requested_cpu":    requestedCPU,
			"requested_memory": requestedMemory,
		},
	}
}

// createIdleDeploymentRecommendation creates a recommendation for an idle deployment
func (r *IdleResourceRecommender) createIdleDeploymentRecommendation(deployment appsv1.Deployment) *IdleResourceRecommendation {
	// Estimate cost based on replica configuration
	replicaCount := int32(0)
	if deployment.Spec.Replicas != nil {
		replicaCount = *deployment.Spec.Replicas
	}

	estimatedCost := 0.0
	if replicaCount > 0 {
		// Estimate based on resource requests
		containers := deployment.Spec.Template.Spec.Containers
		requestedCPU := int64(0)
		requestedMemory := int64(0)

		for _, container := range containers {
			if container.Resources.Requests != nil {
				if cpu, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
					requestedCPU += cpu.MilliValue()
				}
				if memory, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
					requestedMemory += memory.Value()
				}
			}
		}

		estimatedCost = r.estimatePodCost(requestedCPU, requestedMemory) * float64(replicaCount)
	}

	return &IdleResourceRecommendation{
		Type:              IdleDeployment,
		ResourceName:      deployment.Name,
		ResourceNamespace: deployment.Namespace,
		Status:            StatusIdle,
		CurrentCost:       estimatedCost,
		PotentialSavings:  estimatedCost,
		RecommendedAction: "Scale to zero replicas or delete deployment",
		Priority:          4,
		Confidence:        0.85,
		Impact:            "Medium - affects deployment scaling",
		Details: map[string]interface{}{
			"replicas":           replicaCount,
			"available_replicas": deployment.Status.AvailableReplicas,
			"ready_replicas":     deployment.Status.ReadyReplicas,
		},
	}
}

// createIdleNamespaceRecommendation creates a recommendation for an idle namespace
func (r *IdleResourceRecommender) createIdleNamespaceRecommendation(namespace corev1.Namespace, idleDuration time.Duration) *IdleResourceRecommendation {
	return &IdleResourceRecommendation{
		Type:              IdleNamespace,
		ResourceName:      namespace.Name,
		Status:            StatusIdle,
		IdleDuration:      idleDuration,
		CurrentCost:       0, // Namespaces themselves don't incur direct costs
		PotentialSavings:  0,
		RecommendedAction: "Delete idle namespace",
		Priority:          1,
		Confidence:        0.95,
		Impact:            "Low - no active resources in namespace",
		Details: map[string]interface{}{
			"idle_duration": idleDuration.String(),
			"created_at":    namespace.CreationTimestamp.Time,
		},
	}
}

// createUnusedVolumeRecommendation creates a recommendation for an unused volume
func (r *IdleResourceRecommender) createUnusedVolumeRecommendation(pvc corev1.PersistentVolumeClaim) *IdleResourceRecommendation {
	// Estimate storage cost
	storageSize := pvc.Spec.Resources.Requests.Storage().Value()
	monthlyCostPerGB := 0.1 // $0.1 per GB per month
	estimatedCost := float64(storageSize) / (1024 * 1024 * 1024) * monthlyCostPerGB

	return &IdleResourceRecommendation{
		Type:              IdleVolume,
		ResourceName:      pvc.Name,
		ResourceNamespace: pvc.Namespace,
		Status:            StatusReclaimable,
		IdleDuration:      time.Since(pvc.CreationTimestamp.Time),
		CurrentCost:       estimatedCost,
		PotentialSavings:  estimatedCost,
		RecommendedAction: "Delete unused PVC",
		Priority:          2,
		Confidence:        0.9,
		Impact:            "Low - no data in use",
		Details: map[string]interface{}{
			"storage_size":  pvc.Spec.Resources.Requests.Storage().String(),
			"storage_class": pvc.Spec.StorageClassName,
			"access_modes":  pvc.Spec.AccessModes,
		},
	}
}

// createIdleLoadBalancerRecommendation creates a recommendation for an idle load balancer
func (r *IdleResourceRecommender) createIdleLoadBalancerRecommendation(service corev1.Service) *IdleResourceRecommendation {
	// Estimate load balancer cost (varies by cloud provider)
	estimatedCost := 15.0 // $15 per month average

	return &IdleResourceRecommendation{
		Type:              IdleLoadBalancer,
		ResourceName:      service.Name,
		ResourceNamespace: service.Namespace,
		Status:            StatusIdle,
		CurrentCost:       estimatedCost,
		PotentialSavings:  estimatedCost,
		RecommendedAction: "Delete idle load balancer service",
		Priority:          3,
		Confidence:        0.75,
		Impact:            "Low - no active endpoints",
		Details: map[string]interface{}{
			"service_type": service.Spec.Type,
			"ports":        service.Spec.Ports,
		},
	}
}

// calculateIdlePriority calculates priority based on cost
func (r *IdleResourceRecommender) calculateIdlePriority(cost float64) int {
	if cost > 100 {
		return 5
	} else if cost > 50 {
		return 4
	} else if cost > 20 {
		return 3
	} else if cost > 10 {
		return 2
	}
	return 1
}

// estimatePodCost estimates monthly cost for a pod
func (r *IdleResourceRecommender) estimatePodCost(cpuMilliCores, memoryBytes int64) float64 {
	// Simple cost estimation
	costPerMilliCoreMonth := 0.001 // $0.001 per milli-core per month
	costPerMBMonth := 0.0001       // $0.0001 per MB per month

	cpuCost := float64(cpuMilliCores) * costPerMilliCoreMonth
	memoryCost := float64(memoryBytes) / (1024 * 1024) * costPerMBMonth

	return cpuCost + memoryCost
}

// sortRecommendationsByPriority sorts recommendations by potential savings and priority
func (r *IdleResourceRecommender) sortRecommendationsByPriority() {
	// Sort by priority first, then by potential savings
	sort.Slice(r.recommendations, func(i, j int) bool {
		if r.recommendations[i].Priority != r.recommendations[j].Priority {
			return r.recommendations[i].Priority > r.recommendations[j].Priority
		}
		return r.recommendations[i].PotentialSavings > r.recommendations[j].PotentialSavings
	})
}

// getNodeMetrics retrieves current node metrics
func (r *IdleResourceRecommender) getNodeMetrics() (map[string]*ResourceUtilization, error) {
	// Get node metrics from metrics server
	nodeMetrics, err := r.k8sClient.MetricsV1beta1().NodeMetricses().List(r.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node metrics: %w", err)
	}

	metrics := make(map[string]*ResourceUtilization)

	for _, metric := range nodeMetrics.Items {
		// Get node details to calculate percentages
		node, err := r.k8sClient.CoreV1().Nodes().Get(r.ctx, metric.Name, metav1.GetOptions{})
		if err != nil {
			continue
		}

		cpuCapacity := node.Status.Capacity.Cpu()
		memoryCapacity := node.Status.Capacity.Memory()

		metrics[metric.Name] = &ResourceUtilization{
			ActualCPU:         *metric.Usage.Cpu(),
			ActualMemory:      *metric.Usage.Memory(),
			CPUUtilization:    float64(metric.Usage.Cpu().MilliValue()) / float64(cpuCapacity.MilliValue()) * 100,
			MemoryUtilization: float64(metric.Usage.Memory().Value()) / float64(memoryCapacity.Value()) * 100,
		}
	}

	return metrics, nil
}

// GetIdleResourceSummary returns a summary of all idle resources
func (r *IdleResourceRecommender) GetIdleResourceSummary() map[string]interface{} {
	summary := make(map[string]interface{})
	totalSavings := 0.0
	resourceCounts := make(map[IdleResourceType]int)

	for _, rec := range r.recommendations {
		totalSavings += rec.PotentialSavings
		resourceCounts[rec.Type]++
	}

	summary["total_potential_savings"] = totalSavings
	summary["total_recommendations"] = len(r.recommendations)
	summary["idle_resource_counts"] = resourceCounts

	return summary
}
