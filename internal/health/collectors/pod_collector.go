// File: internal/health/collectors/pod_collector.go
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

// PodMetrics represents the metrics collected for a pod
type PodMetrics struct {
	Name              string
	Namespace         string
	Status            string
	Phase             corev1.PodPhase
	RestartCount      int32
	CPUUsage          int64
	MemoryUsage       int64
	CPURequest        int64
	MemoryRequest     int64
	CPULimit          int64
	MemoryLimit       int64
	Node              string
	Age               time.Duration
	CreationTimestamp time.Time
	OwnerReference    string
	OwnerKind         string
	Labels            map[string]string
	StartTime         time.Time
	QOSClass          string
	Conditions        map[string]bool
	ContainerStatuses []ContainerStatus
}

// ContainerStatus represents metrics for individual container
type ContainerStatus struct {
	Name         string
	Status       string
	Ready        bool
	RestartCount int32
	Image        string
	CPU          int64
	Memory       int64
	LastState    string
	StartedAt    time.Time
}

// PodCollector collects health metrics from Kubernetes pods
type PodCollector struct {
	k8sClient *kubernetes.Client
	ctx       context.Context
	metrics   map[string]*PodMetrics // key: namespace/podname
	mu        sync.RWMutex
	stopChan  chan struct{}
}

// NewPodCollector creates a new pod collector
func NewPodCollector(k8sClient *kubernetes.Client) *PodCollector {
	return &PodCollector{
		k8sClient: k8sClient,
		metrics:   make(map[string]*PodMetrics),
		stopChan:  make(chan struct{}),
	}
}

// Name returns the collector name
func (pc *PodCollector) Name() string {
	return "pod-collector"
}

// Start begins collecting pod metrics
func (pc *PodCollector) Start(ctx context.Context) error {
	pc.ctx = ctx

	// Initial collection
	if err := pc.collectPodMetrics(); err != nil {
		log.Printf("Initial pod metrics collection failed: %v", err)
	}

	return nil
}

// Stop halts the collector
func (pc *PodCollector) Stop() {
	close(pc.stopChan)
}

// Collect returns the current pod metrics
func (pc *PodCollector) Collect() (map[string]interface{}, error) {
	// Collect fresh metrics
	if err := pc.collectPodMetrics(); err != nil {
		return nil, fmt.Errorf("failed to collect pod metrics: %w", err)
	}

	pc.mu.RLock()
	defer pc.mu.RUnlock()

	// Convert metrics to interface map
	result := make(map[string]interface{})

	// Overall pod health status
	podStatus := pc.getPodStatusSummary()
	result["pod_health_summary"] = podStatus

	// Namespace breakdown
	namespaceMetrics := pc.getNamespaceMetrics()
	result["namespace_pod_metrics"] = namespaceMetrics

	// Top problematic pods
	result["problematic_pods"] = pc.getProblematicPods(10)

	// Resource utilization metrics
	result["pod_resource_utilization"] = pc.getResourceUtilization()

	// QoS class distribution
	result["qos_distribution"] = pc.getQoSDistribution()

	return result, nil
}

// collectPodMetrics gathers metrics from all pods
func (pc *PodCollector) collectPodMetrics() error {
	// Get pod list from all namespaces
	pods, err := pc.k8sClient.GetPods(pc.ctx, "")
	if err != nil {
		return fmt.Errorf("failed to get pods: %w", err)
	}

	// Get pod metrics from metrics server
	podMetricsList, err := pc.getPodMetrics()
	if err != nil {
		log.Printf("Warning: failed to get pod metrics: %v", err)
		// Continue without resource usage data
	}

	// Create a map for quick lookup of metrics
	metricsMap := make(map[string]v1beta1.PodMetrics)
	for _, metric := range podMetricsList {
		key := fmt.Sprintf("%s/%s", metric.Namespace, metric.Name)
		metricsMap[key] = metric
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Process each pod
	for _, pod := range pods.Items {
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		metrics := pc.processPod(&pod, metricsMap[key])
		pc.metrics[key] = metrics
	}

	return nil
}

// processPod extracts metrics from a pod object
func (pc *PodCollector) processPod(pod *corev1.Pod, metric v1beta1.PodMetrics) *PodMetrics {
	podMetrics := &PodMetrics{
		Name:              pod.Name,
		Namespace:         pod.Namespace,
		Status:            string(pod.Status.Phase),
		Phase:             pod.Status.Phase,
		Node:              pod.Spec.NodeName,
		Age:               time.Since(pod.CreationTimestamp.Time),
		CreationTimestamp: pod.CreationTimestamp.Time,
		Labels:            pod.Labels,
		QOSClass:          string(pod.Status.QOSClass),
		Conditions:        make(map[string]bool),
	}

	// Get start time
	if pod.Status.StartTime != nil {
		podMetrics.StartTime = pod.Status.StartTime.Time
	}

	// Extract owner reference
	if len(pod.OwnerReferences) > 0 {
		owner := pod.OwnerReferences[0]
		podMetrics.OwnerReference = owner.Name
		podMetrics.OwnerKind = owner.Kind
	}

	// Process pod conditions
	for _, condition := range pod.Status.Conditions {
		podMetrics.Conditions[string(condition.Type)] = condition.Status == corev1.ConditionTrue
	}

	// Process container statuses
	for _, containerStatus := range pod.Status.ContainerStatuses {
		cStatus := ContainerStatus{
			Name:         containerStatus.Name,
			Ready:        containerStatus.Ready,
			RestartCount: containerStatus.RestartCount,
			Image:        containerStatus.Image,
		}

		// Get container state
		if containerStatus.State.Running != nil {
			cStatus.Status = "Running"
			cStatus.StartedAt = containerStatus.State.Running.StartedAt.Time
		} else if containerStatus.State.Waiting != nil {
			cStatus.Status = fmt.Sprintf("Waiting: %s", containerStatus.State.Waiting.Reason)
		} else if containerStatus.State.Terminated != nil {
			cStatus.Status = fmt.Sprintf("Terminated: %s", containerStatus.State.Terminated.Reason)
		}

		// Get last terminated state
		if containerStatus.LastTerminationState.Terminated != nil {
			cStatus.LastState = fmt.Sprintf("Terminated: %s", containerStatus.LastTerminationState.Terminated.Reason)
		}

		podMetrics.ContainerStatuses = append(podMetrics.ContainerStatuses, cStatus)
		podMetrics.RestartCount += containerStatus.RestartCount
	}

	// Calculate resource requirements
	for _, container := range pod.Spec.Containers {
		if container.Resources.Requests != nil {
			if cpu := container.Resources.Requests.Cpu(); cpu != nil {
				podMetrics.CPURequest += cpu.MilliValue()
			}
			if memory := container.Resources.Requests.Memory(); memory != nil {
				podMetrics.MemoryRequest += memory.Value()
			}
		}
		if container.Resources.Limits != nil {
			if cpu := container.Resources.Limits.Cpu(); cpu != nil {
				podMetrics.CPULimit += cpu.MilliValue()
			}
			if memory := container.Resources.Limits.Memory(); memory != nil {
				podMetrics.MemoryLimit += memory.Value()
			}
		}
	}

	// Add resource usage if metrics are available
	for _, container := range metric.Containers {
		podMetrics.CPUUsage += container.Usage.Cpu().MilliValue()
		podMetrics.MemoryUsage += container.Usage.Memory().Value()

		// Update container-specific metrics
		for i, cs := range podMetrics.ContainerStatuses {
			if cs.Name == container.Name {
				podMetrics.ContainerStatuses[i].CPU = container.Usage.Cpu().MilliValue()
				podMetrics.ContainerStatuses[i].Memory = container.Usage.Memory().Value()
				break
			}
		}
	}

	return podMetrics
}

// getPodMetrics retrieves resource usage metrics for pods
func (pc *PodCollector) getPodMetrics() ([]v1beta1.PodMetrics, error) {
	metricsClient := pc.k8sClient.MetricsV1beta1().PodMetricses("")

	podMetrics, err := metricsClient.List(pc.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	return podMetrics.Items, nil
}

// getPodStatusSummary returns a summary of pod health status
func (pc *PodCollector) getPodStatusSummary() map[string]interface{} {
	statusCount := make(map[string]int)
	totalPods := len(pc.metrics)
	totals := map[string]int32{
		"restarts": 0,
		"pending":  0,
		"failed":   0,
	}

	for _, metrics := range pc.metrics {
		statusCount[metrics.Status]++
		totals["restarts"] += metrics.RestartCount

		if metrics.Phase == corev1.PodPending {
			totals["pending"]++
		}
		if metrics.Phase == corev1.PodFailed {
			totals["failed"]++
		}
	}

	return map[string]interface{}{
		"total_pods":       totalPods,
		"status_breakdown": statusCount,
		"total_restarts":   totals["restarts"],
		"pending_pods":     totals["pending"],
		"failed_pods":      totals["failed"],
	}
}

// getNamespaceMetrics returns pod metrics by namespace
func (pc *PodCollector) getNamespaceMetrics() map[string]interface{} {
	nsMetrics := make(map[string]interface{})

	for _, metrics := range pc.metrics {
		ns := metrics.Namespace
		if nsMetrics[ns] == nil {
			nsMetrics[ns] = map[string]interface{}{
				"pod_count":      0,
				"running_pods":   0,
				"pending_pods":   0,
				"failed_pods":    0,
				"total_restarts": int32(0),
				"cpu_usage":      int64(0),
				"memory_usage":   int64(0),
			}
		}

		nsData := nsMetrics[ns].(map[string]interface{})
		nsData["pod_count"] = nsData["pod_count"].(int) + 1

		if metrics.Phase == corev1.PodRunning {
			nsData["running_pods"] = nsData["running_pods"].(int) + 1
		}
		if metrics.Phase == corev1.PodPending {
			nsData["pending_pods"] = nsData["pending_pods"].(int) + 1
		}
		if metrics.Phase == corev1.PodFailed {
			nsData["failed_pods"] = nsData["failed_pods"].(int) + 1
		}

		nsData["total_restarts"] = nsData["total_restarts"].(int32) + metrics.RestartCount
		nsData["cpu_usage"] = nsData["cpu_usage"].(int64) + metrics.CPUUsage
		nsData["memory_usage"] = nsData["memory_usage"].(int64) + metrics.MemoryUsage
	}

	return nsMetrics
}

// getProblematicPods returns a list of pods with issues
func (pc *PodCollector) getProblematicPods(limit int) []map[string]interface{} {
	type podIssue struct {
		key      string
		issues   []string
		severity int
	}

	issues := make([]podIssue, 0)

	for key, metrics := range pc.metrics {
		issue := podIssue{key: key}

		// Check for various issues
		if metrics.Phase != corev1.PodRunning {
			issue.issues = append(issue.issues, fmt.Sprintf("Pod phase: %s", metrics.Phase))
			issue.severity += 2
		}

		if metrics.RestartCount > 5 {
			issue.issues = append(issue.issues, fmt.Sprintf("High restart count: %d", metrics.RestartCount))
			issue.severity += 3
		}

		if !metrics.Conditions[string(corev1.PodReady)] {
			issue.issues = append(issue.issues, "Pod not ready")
			issue.severity += 2
		}

		for _, cs := range metrics.ContainerStatuses {
			if !cs.Ready {
				issue.issues = append(issue.issues, fmt.Sprintf("Container %s not ready: %s", cs.Name, cs.Status))
				issue.severity += 1
			}
		}

		if len(issue.issues) > 0 {
			issues = append(issues, issue)
		}
	}

	// Sort by severity
	for i := 0; i < len(issues); i++ {
		for j := i + 1; j < len(issues); j++ {
			if issues[i].severity < issues[j].severity {
				issues[i], issues[j] = issues[j], issues[i]
			}
		}
	}

	// Convert to result format
	result := make([]map[string]interface{}, 0, limit)
	for i := 0; i < len(issues) && i < limit; i++ {
		result = append(result, map[string]interface{}{
			"pod":      issues[i].key,
			"issues":   issues[i].issues,
			"severity": issues[i].severity,
		})
	}

	return result
}

// getResourceUtilization returns resource utilization metrics
func (pc *PodCollector) getResourceUtilization() map[string]interface{} {
	totalCPURequest := int64(0)
	totalMemoryRequest := int64(0)
	totalCPUUsage := int64(0)
	totalMemoryUsage := int64(0)
	totalCPULimit := int64(0)
	totalMemoryLimit := int64(0)

	for _, metrics := range pc.metrics {
		totalCPURequest += metrics.CPURequest
		totalMemoryRequest += metrics.MemoryRequest
		totalCPUUsage += metrics.CPUUsage
		totalMemoryUsage += metrics.MemoryUsage
		totalCPULimit += metrics.CPULimit
		totalMemoryLimit += metrics.MemoryLimit
	}

	return map[string]interface{}{
		"total_cpu_request_millicores": totalCPURequest,
		"total_memory_request_bytes":   totalMemoryRequest,
		"total_cpu_usage_millicores":   totalCPUUsage,
		"total_memory_usage_bytes":     totalMemoryUsage,
		"total_cpu_limit_millicores":   totalCPULimit,
		"total_memory_limit_bytes":     totalMemoryLimit,
		"cpu_utilization_percent":      calculateUtilizationPercent(totalCPUUsage, totalCPURequest),
		"memory_utilization_percent":   calculateUtilizationPercent(totalMemoryUsage, totalMemoryRequest),
	}
}

// getQoSDistribution returns QoS class distribution
func (pc *PodCollector) getQoSDistribution() map[string]int {
	qosCount := make(map[string]int)

	for _, metrics := range pc.metrics {
		qosCount[metrics.QOSClass]++
	}

	return qosCount
}

// calculateUtilizationPercent safely calculates utilization percentage
func calculateUtilizationPercent(usage, request int64) float64 {
	if request == 0 {
		return 0
	}
	return (float64(usage) / float64(request)) * 100
}

// AlertableMetrics returns metrics that could trigger alerts
func (pc *PodCollector) AlertableMetrics() map[string]interface{} {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	alerts := make(map[string]interface{})

	for key, metrics := range pc.metrics {
		podAlerts := make(map[string]interface{})

		// Check for pod failure
		if metrics.Phase == corev1.PodFailed {
			podAlerts["pod_failed"] = fmt.Sprintf("Pod in Failed state: %s", metrics.Status)
		}

		// Check for high restart count
		if metrics.RestartCount > 5 {
			podAlerts["high_restarts"] = fmt.Sprintf("Pod has %d restarts", metrics.RestartCount)
		}

		// Check for pending pods older than 5 minutes
		if metrics.Phase == corev1.PodPending && metrics.Age > 5*time.Minute {
			podAlerts["pending_long"] = fmt.Sprintf("Pod pending for %v", metrics.Age)
		}

		// Check for resource overcommitment
		if metrics.CPUUsage > 0 && metrics.CPULimit > 0 {
			cpuUtilization := (float64(metrics.CPUUsage) / float64(metrics.CPULimit)) * 100
			if cpuUtilization > 90 {
				podAlerts["cpu_overload"] = fmt.Sprintf("CPU usage at %.1f%% of limit", cpuUtilization)
			}
		}

		if metrics.MemoryUsage > 0 && metrics.MemoryLimit > 0 {
			memoryUtilization := (float64(metrics.MemoryUsage) / float64(metrics.MemoryLimit)) * 100
			if memoryUtilization > 90 {
				podAlerts["memory_overload"] = fmt.Sprintf("Memory usage at %.1f%% of limit", memoryUtilization)
			}
		}

		if len(podAlerts) > 0 {
			alerts[key] = podAlerts
		}
	}

	return alerts
}
