// File: internal/health/collectors/controlplane_collector.go
package collectors

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"
	
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/apimachinery/pkg/version"
)

// ComponentMetrics represents the metrics for a control plane component
type ComponentMetrics struct {
	Name              string
	Status            string
	Healthy           bool
	Message           string
	ResponseTime      time.Duration
	LastCheck         time.Time
	EndpointReachable bool
	ErrorCount        int
	PodStatus         string
	PodIP             string
	NodeName          string
	ContainerStatuses []ContainerStatus
	Age               time.Duration
}

// ControlPlaneMetrics represents overall control plane health
type ControlPlaneMetrics struct {
	APIServerMetrics        *ComponentMetrics
	ControllerManagerStatus *ComponentMetrics
	SchedulerStatus         *ComponentMetrics
	EtcdStatus              *ComponentMetrics
	OverallStatus           string
	KubernetesVersion       *version.Info
	LastHealthCheck         time.Time
	ClusterAPIErrors        int
	LeaderElection          map[string]string
}

// ControlPlaneCollector collects health metrics from control plane components
type ControlPlaneCollector struct {
	k8sClient *kubernetes.Client
	ctx       context.Context
	config    *rest.Config
	metrics   *ControlPlaneMetrics
	mu        sync.RWMutex
	stopChan  chan struct{}
	httpClient *http.Client
}

// NewControlPlaneCollector creates a new control plane collector
func NewControlPlaneCollector(k8sClient *kubernetes.Client) *ControlPlaneCollector {
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	return &ControlPlaneCollector{
		k8sClient: k8sClient,
		stopChan:  make(chan struct{}),
		httpClient: httpClient,
		metrics: &ControlPlaneMetrics{
			LeaderElection: make(map[string]string),
		},
	}
}

// Name returns the collector name
func (cpc *ControlPlaneCollector) Name() string {
	return "controlplane-collector"
}

// Start begins collecting control plane metrics
func (cpc *ControlPlaneCollector) Start(ctx context.Context) error {
	cpc.ctx = ctx
	cpc.config = cpc.k8sClient.RESTConfig()
	
	// Initial collection
	if err := cpc.collectControlPlaneMetrics(); err != nil {
		log.Printf("Initial control plane metrics collection failed: %v", err)
	}
	
	return nil
}

// Stop halts the collector
func (cpc *ControlPlaneCollector) Stop() {
	close(cpc.stopChan)
}

// Collect returns the current control plane metrics
func (cpc *ControlPlaneCollector) Collect() (map[string]interface{}, error) {
	// Collect fresh metrics
	if err := cpc.collectControlPlaneMetrics(); err != nil {
		return nil, fmt.Errorf("failed to collect control plane metrics: %w", err)
	}
	
	cpc.mu.RLock()
	defer cpc.mu.RUnlock()
	
	// Convert metrics to interface map
	result := make(map[string]interface{})
	
	result["control_plane_status"] = cpc.metrics.OverallStatus
	result["api_server"] = cpc.componentToMap(cpc.metrics.APIServerMetrics)
	result["controller_manager"] = cpc.componentToMap(cpc.metrics.ControllerManagerStatus)
	result["scheduler"] = cpc.componentToMap(cpc.metrics.SchedulerStatus)
	result["etcd"] = cpc.componentToMap(cpc.metrics.EtcdStatus)
	result["kubernetes_version"] = cpc.metrics.KubernetesVersion
	result["last_health_check"] = cpc.metrics.LastHealthCheck
	result["cluster_api_errors"] = cpc.metrics.ClusterAPIErrors
	result["leader_election"] = cpc.metrics.LeaderElection
	
	return result, nil
}

// collectControlPlaneMetrics gathers metrics from all control plane components
func (cpc *ControlPlaneCollector) collectControlPlaneMetrics() error {
	cpc.mu.Lock()
	defer cpc.mu.Unlock()
	
	// Check API server health
	apiServerMetrics := cpc.checkAPIServerHealth()
	cpc.metrics.APIServerMetrics = apiServerMetrics
	
	// Get Kubernetes version
	cpc.getClusterVersion()
	
	// Check other control plane components
	cpc.checkControllerManager()
	cpc.checkScheduler()
	cpc.checkEtcd()
	
	// Determine overall status
	cpc.determineOverallStatus()
	
	// Update last check time
	cpc.metrics.LastHealthCheck = time.Now()
	
	return nil
}

// checkAPIServerHealth checks the health of the API server
func (cpc *ControlPlaneCollector) checkAPIServerHealth() *ComponentMetrics {
	metrics := &ComponentMetrics{
		Name:      "api-server",
		LastCheck: time.Now(),
	}
	
	start := time.Now()
	
	// Check API server livez endpoint
	livezPath := "/livez"
	err := cpc.checkHealthEndpoint(livezPath)
	if err != nil {
		metrics.Healthy = false
		metrics.Status = "Unhealthy"
		metrics.Message = fmt.Sprintf("Livez check failed: %v", err)
		metrics.ErrorCount++
		return metrics
	}
	
	// Check API server readyz endpoint
	readyzPath := "/readyz"
	err = cpc.checkHealthEndpoint(readyzPath)
	if err != nil {
		metrics.Healthy = false
		metrics.Status = "NotReady"
		metrics.Message = fmt.Sprintf("Readyz check failed: %v", err)
		metrics.ErrorCount++
		return metrics
	}
	
	metrics.ResponseTime = time.Since(start)
	metrics.Healthy = true
	metrics.Status = "Healthy"
	metrics.Message = "All API server checks passed"
	metrics.EndpointReachable = true
	
	// Check API server pods
	cpc.checkAPIServerPods(metrics)
	
	return metrics
}

// checkControllerManager checks the controller manager health
func (cpc *ControlPlaneCollector) checkControllerManager() {
	metrics := &ComponentMetrics{
		Name:      "controller-manager",
		LastCheck: time.Now(),
	}
	
	// Try to find controller manager pods
	pods, err := cpc.getControlPlaneComponentPods("kube-controller-manager")
	if err != nil || len(pods) == 0 {
		metrics.Healthy = false
		metrics.Status = "NotFound"
		metrics.Message = "Controller manager pods not found"
		cpc.metrics.ControllerManagerStatus = metrics
		return
	}
	
	// Check if controller manager is running
	componentFound := false
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning {
			componentFound = true
			metrics.Healthy = true
			metrics.Status = "Running"
			metrics.PodStatus = string(pod.Status.Phase)
			metrics.NodeName = pod.Spec.NodeName
			metrics.Age = time.Since(pod.CreationTimestamp.Time)
			metrics.Message = "Controller Manager running"
			break
		}
	}
	
	if !componentFound {
		metrics.Healthy = false
		metrics.Status = "NotRunning"
		metrics.Message = "Controller Manager not in running state"
	}
	
	// Check for leader election
	cpc.checkLeaderElection("kube-controller-manager", metrics)
	
	cpc.metrics.ControllerManagerStatus = metrics
}

// checkScheduler checks the scheduler health
func (cpc *ControlPlaneCollector) checkScheduler() {
	metrics := &ComponentMetrics{
		Name:      "scheduler",
		LastCheck: time.Now(),
	}
	
	// Try to find scheduler pods
	pods, err := cpc.getControlPlaneComponentPods("kube-scheduler")
	if err != nil || len(pods) == 0 {
		metrics.Healthy = false
		metrics.Status = "NotFound"
		metrics.Message = "Scheduler pods not found"
		cpc.metrics.SchedulerStatus = metrics
		return
	}
	
	// Check if scheduler is running
	componentFound := false
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning {
			componentFound = true
			metrics.Healthy = true
			metrics.Status = "Running"
			metrics.PodStatus = string(pod.Status.Phase)
			metrics.NodeName = pod.Spec.NodeName
			metrics.Age = time.Since(pod.CreationTimestamp.Time)
			metrics.Message = "Scheduler running"
			break
		}
	}
	
	if !componentFound {
		metrics.Healthy = false
		metrics.Status = "NotRunning"
		metrics.Message = "Scheduler not in running state"
	}
	
	// Check for leader election
	cpc.checkLeaderElection("kube-scheduler", metrics)
	
	cpc.metrics.SchedulerStatus = metrics
}

// checkEtcd checks the etcd cluster health
func (cpc *ControlPlaneCollector) checkEtcd() {
	metrics := &ComponentMetrics{
		Name:      "etcd",
		LastCheck: time.Now(),
	}
	
	// Try to find etcd pods
	pods, err := cpc.getControlPlaneComponentPods("etcd")
	if err != nil || len(pods) == 0 {
		metrics.Healthy = false
		metrics.Status = "NotFound"
		metrics.Message = "etcd pods not found"
		cpc.metrics.EtcdStatus = metrics
		return
	}
	
	// Check etcd pod status
	runningCount := 0
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning {
			runningCount++
		}
	}
	
	// etcd should be running (usually 3 nodes for HA)
	if runningCount > 0 {
		metrics.Healthy = true
		metrics.Status = "Running"
		metrics.Message = fmt.Sprintf("%d/%d etcd members running", runningCount, len(pods))
		metrics.NodeName = pods[0].Spec.NodeName
		metrics.Age = time.Since(pods[0].CreationTimestamp.Time)
	} else {
		metrics.Healthy = false
		metrics.Status = "NotRunning"
		metrics.Message = "No etcd members running"
	}
	
// checkAPIServerPods checks API server pods
func (cpc *ControlPlaneCollector) checkAPIServerPods(metrics *ComponentMetrics) {
	pods, err := cpc.getControlPlaneComponentPods("kube-apiserver")
	if err != nil || len(pods) == 0 {
		return
	}
	
	for _, pod := range pods {
		metrics.NodeName = pod.Spec.NodeName
		metrics.PodStatus = string(pod.Status.Phase)
		metrics.Age = time.Since(pod.CreationTimestamp.Time)
		metrics.PodIP = pod.Status.PodIP
		
		// Get container statuses
		for _, containerStatus := range pod.Status.ContainerStatuses {
			cs := ContainerStatus{
				Name:         containerStatus.Name,
				Ready:        containerStatus.Ready,
				RestartCount: containerStatus.RestartCount,
				Image:        containerStatus.Image,
			}
			
			if containerStatus.State.Running != nil {
				cs.Status = "Running"
				cs.StartedAt = containerStatus.State.Running.StartedAt.Time
			} else if containerStatus.State.Waiting != nil {
				cs.Status = fmt.Sprintf("Waiting: %s", containerStatus.State.Waiting.Reason)
			} else if containerStatus.State.Terminated != nil {
				cs.Status = fmt.Sprintf("Terminated: %s", containerStatus.State.Terminated.Reason)
			}
			
			metrics.ContainerStatuses = append(metrics.ContainerStatuses, cs)
		}
		break // Just check the first API server pod for details
	}
}

// checkHealthEndpoint checks a specific health endpoint
func (cpc *ControlPlaneCollector) checkHealthEndpoint(path string) error {
	// Prepare the request URL
	url := fmt.Sprintf("%s%s", cpc.config.Host, path)
	
	// Create request with timeout
	req, err := http.NewRequestWithContext(cpc.ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	// Set appropriate headers
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", cpc.config.BearerToken))
	
	// Make the request
	resp, err := cpc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach endpoint: %w", err)
	}
	defer resp.Body.Close()
	
	// Check status code
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("endpoint returned status code: %d", resp.StatusCode)
	}
	
	return nil
}

// getControlPlaneComponentPods retrieves pods for a specific control plane component
func (cpc *ControlPlaneCollector) getControlPlaneComponentPods(componentName string) ([]corev1.Pod, error) {
	// Try multiple namespaces where control plane components might be
	namespaces := []string{"kube-system", "kube-public", "default"}
	var allPods []corev1.Pod
	
	for _, namespace := range namespaces {
		pods, err := cpc.k8sClient.GetPods(cpc.ctx, namespace)
		if err != nil {
			continue
		}
		
		for _, pod := range pods.Items {
			// Check if pod is a control plane component
			if pod.Labels["component"] == componentName ||
			   pod.Labels["k8s-app"] == componentName ||
			   pod.Name == componentName {
				allPods = append(allPods, pod)
			}
		}
	}
	
	return allPods, nil
}

// checkLeaderElection checks leader election status for components
func (cpc *ControlPlaneCollector) checkLeaderElection(componentName string, metrics *ComponentMetrics) {
	// Query endpoints to check leader election
	endpoints, err := cpc.k8sClient.CoreV1().Endpoints("kube-system").Get(cpc.ctx, componentName, metav1.GetOptions{})
	if err != nil {
		return
	}
	
	// Check annotations for leader election
	if endpoints.Annotations != nil {
		if holderIdentity, ok := endpoints.Annotations["control-plane.alpha.kubernetes.io/leader"]; ok {
			cpc.metrics.LeaderElection[componentName] = holderIdentity
		}
	}
}

// getClusterVersion retrieves the Kubernetes cluster version
func (cpc *ControlPlaneCollector) getClusterVersion() {
	// Create discovery client
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cpc.config)
	if err != nil {
		log.Printf("Failed to create discovery client: %v", err)
		return
	}
	
	// Get server version
	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		log.Printf("Failed to get server version: %v", err)
		return
	}
	
	cpc.metrics.KubernetesVersion = serverVersion
}

// determineOverallStatus determines the overall control plane health status
func (cpc *ControlPlaneCollector) determineOverallStatus() {
	healthyCount := 0
	totalComponents := 0
	
	components := []*ComponentMetrics{
		cpc.metrics.APIServerMetrics,
		cpc.metrics.ControllerManagerStatus,
		cpc.metrics.SchedulerStatus,
		cpc.metrics.EtcdStatus,
	}
	
	for _, component := range components {
		if component != nil {
			totalComponents++
			if component.Healthy {
				healthyCount++
			}
		}
	}
	
	if healthyCount == totalComponents {
		cpc.metrics.OverallStatus = "Healthy"
	} else if healthyCount > 0 {
		cpc.metrics.OverallStatus = "Degraded"
	} else {
		cpc.metrics.OverallStatus = "Unhealthy"
	}
}

// componentToMap converts ComponentMetrics to map for JSON serialization
func (cpc *ControlPlaneCollector) componentToMap(metrics *ComponentMetrics) map[string]interface{} {
	if metrics == nil {
		return nil
	}
	
	containerStatuses := make([]map[string]interface{}, 0, len(metrics.ContainerStatuses))
	for _, cs := range metrics.ContainerStatuses {
		containerStatuses = append(containerStatuses, map[string]interface{}{
			"name":           cs.Name,
			"status":         cs.Status,
			"ready":          cs.Ready,
			"restart_count":  cs.RestartCount,
			"image":          cs.Image,
			"cpu":            cs.CPU,
			"memory":         cs.Memory,
			"last_state":     cs.LastState,
			"started_at":     cs.StartedAt,
		})
	}
	
	return map[string]interface{}{
		"name":                metrics.Name,
		"status":              metrics.Status,
		"healthy":             metrics.Healthy,
		"message":             metrics.Message,
		"response_time_ms":    metrics.ResponseTime.Milliseconds(),
		"last_check":          metrics.LastCheck,
		"endpoint_reachable":  metrics.EndpointReachable,
		"error_count":         metrics.ErrorCount,
		"pod_status":          metrics.PodStatus,
		"pod_ip":              metrics.PodIP,
		"node_name":           metrics.NodeName,
		"container_statuses":  containerStatuses,
		"age":                 metrics.Age.String(),
	}
}

// AlertableMetrics returns metrics that could trigger alerts
func (cpc *ControlPlaneCollector) AlertableMetrics() map[string]interface{} {
	cpc.mu.RLock()
	defer cpc.mu.RUnlock()
	
	alerts := make(map[string]interface{})
	
	// Check overall control plane status
	if cpc.metrics.OverallStatus != "Healthy" {
		alerts["control_plane_status"] = fmt.Sprintf("Control plane is %s", cpc.metrics.OverallStatus)
	}
	
	// Check individual components
	components := map[string]*ComponentMetrics{
		"api_server":          cpc.metrics.APIServerMetrics,
		"controller_manager":  cpc.metrics.ControllerManagerStatus,
		"scheduler":           cpc.metrics.SchedulerStatus,
		"etcd":               cpc.metrics.EtcdStatus,
	}
	
	for name, component := range components {
		if component != nil && !component.Healthy {
			alerts[name] = component.Message
		}
	}
	
	// Check for API errors
	if cpc.metrics.ClusterAPIErrors > 10 {
		alerts["api_errors"] = fmt.Sprintf("High number of API errors: %d", cpc.metrics.ClusterAPIErrors)
	}
	
	// Check for leader election issues
	for component, leader := range cpc.metrics.LeaderElection {
		if leader == "" {
			alerts[fmt.Sprintf("leader_election_%s", component)] = fmt.Sprintf("No leader elected for %s", component)
		}
	}
	
	return alerts
}

// MonitorAPIErrors tracks API server errors
func (cpc *ControlPlaneCollector) MonitorAPIErrors() {
	// This would be called from a rate limiter or error handler to track API errors
	cpc.mu.Lock()
	defer cpc.mu.Unlock()
	cpc.metrics.ClusterAPIErrors++
}

// ResetErrorCount resets the API error count
func (cpc *ControlPlaneCollector) ResetErrorCount() {
	cpc.mu.Lock()
	defer cpc.mu.Unlock()
	cpc.metrics.ClusterAPIErrors = 0
}

// GetControlPlaneStatus returns the current control plane status
func (cpc *ControlPlaneCollector) GetControlPlaneStatus() string {
	cpc.mu.RLock()
	defer cpc.mu.RUnlock()
	return cpc.metrics.OverallStatus
}

// CheckAPIServerEndpoint specifically checks a custom API endpoint
func (cpc *ControlPlaneCollector) CheckAPIServerEndpoint(endpoint string) error {
	return cpc.checkHealthEndpoint(endpoint)
}

// GetLeaderElectionStatus returns current leader election status
func (cpc *ControlPlaneCollector) GetLeaderElectionStatus() map[string]string {
	cpc.mu.RLock()
	defer cpc.mu.RUnlock()
	
	result := make(map[string]string)
	for k, v := range cpc.metrics.LeaderElection {
		result[k] = v
	}
	return result
}

//