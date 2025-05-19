package kubernetes

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/metrics/pkg/client/clientset/versioned"

	corev1 "k8s.io/api/core/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	//	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedappsv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsapi "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"
)

// Client provides access to the Kubernetes API
type Client struct {
	clientset     *kubernetes.Clientset
	metricsClient *versioned.Clientset
	config        *rest.Config
	kubeconfig    string
	inCluster     bool
}

// NewClient creates a new Kubernetes client
func NewClient(config Config) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if config.InCluster {
		// Use in-cluster config when running in a pod
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
		}
	} else {
		// Use kubeconfig file
		restConfig, err = clientcmd.BuildConfigFromFlags("", config.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	// Create metrics client
	metricsClient, err := versioned.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}

	return &Client{
		clientset:     clientset,
		metricsClient: metricsClient,
		config:        restConfig,
		kubeconfig:    config.Kubeconfig,
		inCluster:     config.InCluster,
	}, nil
}

// Config holds Kubernetes client configuration
type Config struct {
	Kubeconfig string
	InCluster  bool
}

// RESTConfig returns the REST config for the Kubernetes API
func (c *Client) RESTConfig() *rest.Config {
	return c.config
}

// CoreV1 returns the CoreV1 API interface
func (c *Client) CoreV1() typedcorev1.CoreV1Interface {
	return c.clientset.CoreV1()
}

// AppsV1 returns the AppsV1 API interface
func (c *Client) AppsV1() typedappsv1.AppsV1Interface {
	return c.clientset.AppsV1()
}

// MetricsV1beta1 returns the metrics API interface
func (c *Client) MetricsV1beta1() metricsapi.MetricsV1beta1Interface {
	return c.metricsClient.MetricsV1beta1()
}

// GetNodes returns all nodes in the cluster
func (c *Client) GetNodes(ctx context.Context) (*corev1.NodeList, error) {
	return c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
}

// GetPods returns all pods in the cluster or in a specific namespace
func (c *Client) GetPods(ctx context.Context, namespace string) (*corev1.PodList, error) {
	if namespace != "" {
		return c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	}
	return c.clientset.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
}

// GetClusterResources retrieves all resource information for cost analysis
func (c *Client) GetClusterResources() (map[string]interface{}, error) {
	ctx := context.Background()

	// Get nodes
	nodes, err := c.GetNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	// Get pods
	pods, err := c.GetPods(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}

	// Get node metrics
	nodeMetrics, err := c.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		// Log error but continue without metrics
		nodeMetrics = &metricsv1beta1.NodeMetricsList{}
	}

	// Get pod metrics
	podMetrics, err := c.metricsClient.MetricsV1beta1().PodMetricses(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Log error but continue without metrics
		podMetrics = &metricsv1beta1.PodMetricsList{}
	}

	// Convert to appropriate slice types for the interface
	nodeItems := make([]interface{}, len(nodes.Items))
	for i, node := range nodes.Items {
		nodeItems[i] = node
	}

	podItems := make([]interface{}, len(pods.Items))
	for i, pod := range pods.Items {
		podItems[i] = pod
	}

	nodeMetricItems := make([]interface{}, len(nodeMetrics.Items))
	for i, m := range nodeMetrics.Items {
		nodeMetricItems[i] = m
	}

	podMetricItems := make([]interface{}, len(podMetrics.Items))
	for i, m := range podMetrics.Items {
		podMetricItems[i] = m
	}

	// Build resource map
	resources := map[string]interface{}{
		"nodes":       nodeItems,
		"pods":        podItems,
		"nodeMetrics": nodeMetricItems,
		"podMetrics":  podMetricItems,
	}

	return resources, nil
}
