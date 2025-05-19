// File: internal/health/collectors/node_collector.go
package collectors

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// NodeMetrics represents the metrics collected for a node
type NodeMetrics struct {
	Name                     string
	Status                   string
	CPUCapacity              int64
	MemoryCapacity           int64
	CPUUsage                 int64
	MemoryUsage              int64
	CPUUtilizationPercent    float64
	MemoryUtilizationPercent float64
	PodCount                 int
	PodCapacity              int
	Conditions               map[string]bool
	Age                      time.Duration
	KernelVersion            string
	OSImage                  string
	ContainerRuntimeVersion  string
	KubeletVersion           string
	LastHeartbeat            time.Time
}

// NodeCollector collects health metrics from Kubernetes nodes
type NodeCollector struct {
	k8sClient *kubernetes.Client
	ctx       context.Context
	metrics   map[string]*NodeMetrics
	mu        sync.RWMutex
	stopChan  chan struct{}
}

// NewNodeCollector creates a new node collector
func NewNodeCollector(k8sClient *kubernetes.Client) *NodeCollector {
	return &NodeCollector{
		k8sClient: k8sClient,
		metrics:   make(map[string]*NodeMetrics),
		stopChan:  make(chan struct{}),
	}
}

// Name returns the collector name
func (nc *NodeCollector) Name() string {
	return "node-collector"
}

// Start begins collecting node metrics
func (nc *NodeCollector) Start(ctx context.Context) error {
	nc.ctx = ctx

	// Initial collection
	if err := nc.collectNodeMetrics(); err != nil {
		log.Printf("Initial node metrics collection failed: %v", err)
	}

	return nil
}

// Stop halts the collector
func (nc *NodeCollector) Stop() {
	close(nc.stopChan)
}

// Collect returns the current node metrics
func (nc *NodeCollector) Collect() (map[string]interface{}, error) {
	// Collect fresh metrics
	if err := nc.collectNodeMetrics(); err != nil {
		return nil, fmt.Errorf("failed to collect node metrics: %w", err)
	}

	nc.mu.RLock()
	defer nc.mu.RUnlock()

	// Convert metrics to interface map
	result := make(map[string]interface{})

	// Overall node health status
	healthyNodes := 0
	totalNodes := len(nc.metrics)
	nodeDetails := make(map[string]interface{})

	for name, metrics := range nc.metrics {
		if metrics.Status == "Ready" {
			healthyNodes++
		}

		nodeDetails[name] = map[string]interface{}{
			"status":                     metrics.Status,
			"cpu_utilization_percent":    metrics.CPUUtilizationPercent,
			"memory_utilization_percent": metrics.MemoryUtilizationPercent,
			"pod_count":                  metrics.PodCount,
			"pod_capacity":               metrics.PodCapacity,
			"kernel_version":             metrics.KernelVersion,
			"os_image":                   metrics.OSImage,
			"container_runtime":          metrics.ContainerRuntimeVersion,
			"kubelet_version":            metrics.KubeletVersion,
			"age":                        metrics.Age.String(),
			"conditions":                 metrics.Conditions,
			"last_heartbeat":             metrics.LastHeartbeat,
		}
	}

	result["node_health_summary"] = map[string]interface{}{
		"total_nodes":       totalNodes,
		"healthy_nodes":     healthyNodes,
		"unhealthy_nodes":   totalNodes - healthyNodes,
		"health_percentage": calculatePercentage(healthyNodes, totalNodes),
	}
	result["node_details"] = nodeDetails

	// Calculate average utilization across nodes
	avgCPU, avgMemory := nc.calculateAverageUtilization()
	result["cluster_average_utilization"] = map[string]interface{}{
		"cpu_percent":    avgCPU,
		"memory_percent": avgMemory,
	}

	return result, nil
}

// collectNodeMetrics gathers metrics from all nodes
func (nc *NodeCollector) collectNodeMetrics() error {
	// Get node list
	nodes, err := nc.k8sClient.GetNodes(nc.ctx)
	if err != nil {
		return fmt.Errorf("failed to get nodes: %w", err)
	}

	// Get node metrics from metrics server
	nodeMetrics, err := nc.getNodeMetrics()
	if err != nil {
		log.Printf("Warning: failed to get node metrics: %v", err)
		// Continue without resource usage data
	}

	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Process each node
	for _, node := range nodes.Items {
		metrics := nc.processNode(&node, nodeMetrics)
		nc.metrics[node.Name] = metrics
	}

	return nil
}

// processNode extracts metrics from a node object
func (nc *NodeCollector) processNode(node *corev1.Node, metrics []v1beta1.NodeMetrics) *NodeMetrics {
	nodeMetrics := &NodeMetrics{
		Name:                    node.Name,
		Status:                  nc.getNodeStatus(node),
		Conditions:              make(map[string]bool),
		Age:                     time.Since(node.CreationTimestamp.Time),
		KernelVersion:           node.Status.NodeInfo.KernelVersion,
		OSImage:                 node.Status.NodeInfo.OSImage,
		ContainerRuntimeVersion: node.Status.NodeInfo.ContainerRuntimeVersion,
		KubeletVersion:          node.Status.NodeInfo.KubeletVersion,
	}

	// Extract capacity information
	nodeMetrics.CPUCapacity = node.Status.Capacity.Cpu().MilliValue()
	nodeMetrics.MemoryCapacity = node.Status.Capacity.Memory().Value()

	// Extract pod capacity
	if maxPods, ok := node.Status.Capacity[corev1.ResourcePods]; ok {
		nodeMetrics.PodCapacity = int(maxPods.Value())
	}

	// Process conditions
	for _, condition := range node.Status.Conditions {
		nodeMetrics.Conditions[string(condition.Type)] = condition.Status == corev1.ConditionTrue

		// Update last heartbeat from NodeReady condition
		if condition.Type == corev1.NodeReady {
			nodeMetrics.LastHeartbeat = condition.LastHeartbeatTime.Time
		}
	}

	// Find pod count
	nodeMetrics.PodCount = nc.getPodCountForNode(node.Name)

	// Find resource usage if metrics are available
	for _, metric := range metrics {
		if metric.Name == node.Name {
			nodeMetrics.CPUUsage = metric.Usage.Cpu().MilliValue()
			nodeMetrics.MemoryUsage = metric.Usage.Memory().Value()

			nodeMetrics.CPUUtilizationPercent = calculatePercentage(
				int(nodeMetrics.CPUUsage),
				int(nodeMetrics.CPUCapacity),
			)
			nodeMetrics.MemoryUtilizationPercent = calculatePercentage(
				int(nodeMetrics.MemoryUsage),
				int(nodeMetrics.MemoryCapacity),
			)
			break
		}
	}

	return nodeMetrics
}

// getNodeStatus determines the overall status of a node
func (nc *NodeCollector) getNodeStatus(node *corev1.Node) string {
	// Check if node is scheduled for deletion
	if node.DeletionTimestamp != nil {
		return "Terminating"
	}

	// Check taints that prevent scheduling
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			if taint.Key == "node.kubernetes.io/not-ready" || taint.Key == "node.kubernetes.io/unreachable" {
				return "NotReady"
			}
		}
	}

	// Check Ready condition
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			if condition.Status == corev1.ConditionTrue {
				return "Ready"
			} else {
				return "NotReady"
			}
		}
	}

	return "Unknown"
}

// getNodeMetrics retrieves resource usage metrics for nodes
func (nc *NodeCollector) getNodeMetrics() ([]v1beta1.NodeMetrics, error) {
	metricsClient := nc.k8sClient.MetricsV1beta1().NodeMetricses()

	nodeMetrics, err := metricsClient.List(nc.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node metrics: %w", err)
	}

	return nodeMetrics.Items, nil
}

// getPodCountForNode counts pods running on a specific node
func (nc *NodeCollector) getPodCountForNode(nodeName string) int {
	pods, err := nc.k8sClient.CoreV1().Pods("").List(nc.ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		log.Printf("Failed to get pod count for node %s: %v", nodeName, err)
		return 0
	}

	count := 0
	for _, pod := range pods.Items {
		// Count only running or pending pods
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
			count++
		}
	}

	return count
}

// calculateAverageUtilization computes the average resource utilization across all nodes
func (nc *NodeCollector) calculateAverageUtilization() (float64, float64) {
	if len(nc.metrics) == 0 {
		return 0, 0
	}

	var totalCPU, totalMemory float64
	count := 0

	for _, metrics := range nc.metrics {
		if metrics.CPUUtilizationPercent > 0 || metrics.MemoryUtilizationPercent > 0 {
			totalCPU += metrics.CPUUtilizationPercent
			totalMemory += metrics.MemoryUtilizationPercent
			count++
		}
	}

	if count == 0 {
		return 0, 0
	}

	return totalCPU / float64(count), totalMemory / float64(count)
}

// calculatePercentage safely calculates a percentage
func calculatePercentage(used, total int) float64 {
	if total == 0 {
		return 0
	}
	return (float64(used) / float64(total)) * 100
}

// GetNodeHealth returns health status for a specific node
func (nc *NodeCollector) GetNodeHealth(nodeName string) (*NodeMetrics, error) {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	metrics, exists := nc.metrics[nodeName]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeName)
	}

	return metrics, nil
}

// AlertableMetrics returns metrics that could trigger alerts
func (nc *NodeCollector) AlertableMetrics() map[string]interface{} {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	alerts := make(map[string]interface{})

	for nodeName, metrics := range nc.metrics {
		nodeAlerts := make(map[string]interface{})

		if metrics.Status != "Ready" {
			nodeAlerts["status"] = fmt.Sprintf("Node is %s", metrics.Status)
		}

		if metrics.CPUUtilizationPercent > 80 {
			nodeAlerts["cpu_high"] = fmt.Sprintf("CPU usage at %.1f%%", metrics.CPUUtilizationPercent)
		}

		if metrics.MemoryUtilizationPercent > 85 {
			nodeAlerts["memory_high"] = fmt.Sprintf("Memory usage at %.1f%%", metrics.MemoryUtilizationPercent)
		}

		if float64(metrics.PodCount)/float64(metrics.PodCapacity) > 0.9 {
			nodeAlerts["pod_capacity"] = fmt.Sprintf("Pod capacity at %.0f%%",
				(float64(metrics.PodCount)/float64(metrics.PodCapacity))*100)
		}

		if !metrics.Conditions[string(corev1.NodeDiskPressure)] {
			nodeAlerts["disk_pressure"] = "Node experiencing disk pressure"
		}

		if !metrics.Conditions[string(corev1.NodeMemoryPressure)] {
			nodeAlerts["memory_pressure"] = "Node experiencing memory pressure"
		}

		// Check if node is unresponsive (no heartbeat for 5 minutes)
		if time.Since(metrics.LastHeartbeat) > 5*time.Minute {
			nodeAlerts["unresponsive"] = fmt.Sprintf("No heartbeat for %v", time.Since(metrics.LastHeartbeat))
		}

		if len(nodeAlerts) > 0 {
			alerts[nodeName] = nodeAlerts
		}
	}

	return alerts
}
