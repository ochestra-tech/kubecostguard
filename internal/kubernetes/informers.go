package kubernetes

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/metrics/pkg/client/clientset/versioned"
)

// InformerManager manages all Kubernetes informers
type InformerManager struct {
	client        *Client
	factory       informers.SharedInformerFactory
	metricsClient *versioned.Clientset
	ctx           context.Context
	cancel        context.CancelFunc
	started       bool
	startupWg     sync.WaitGroup
	mu            sync.RWMutex

	// Informers
	nodeInformer        cache.SharedIndexInformer
	podInformer         cache.SharedIndexInformer
	deploymentInformer  cache.SharedIndexInformer
	statefulSetInformer cache.SharedIndexInformer
	serviceInformer     cache.SharedIndexInformer
	namespaceInformer   cache.SharedIndexInformer
	pvcInformer         cache.SharedIndexInformer
	hpaInformer         cache.SharedIndexInformer
	jobInformer         cache.SharedIndexInformer

	// Caches for current state
	nodeCache       map[string]*corev1.Node
	podCache        map[string]*corev1.Pod
	deploymentCache map[string]interface{}
	serviceCache    map[string]*corev1.Service
	namespaceCache  map[string]*corev1.Namespace
	pvcCache        map[string]*corev1.PersistentVolumeClaim

	// Cache mutex
	cacheMu sync.RWMutex

	// Event handlers
	nodeHandlers       []cache.ResourceEventHandler
	podHandlers        []cache.ResourceEventHandler
	deploymentHandlers []cache.ResourceEventHandler
}

// NewInformerManager creates a new informer manager
func NewInformerManager(client *Client) (*InformerManager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create informer factory with 0 resync period to avoid unnecessary full resyncs
	factory := informers.NewSharedInformerFactory(client.clientset, 0)

	// Create metrics client
	var config *rest.Config
	var err error

	if client.config.InCluster {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", client.config.Kubeconfig)
	}

	if err != nil {
		cancel()
		return nil, err
	}

	metricsClient, err := versioned.NewForConfig(config)
	if err != nil {
		cancel()
		return nil, err
	}

	im := &InformerManager{
		client:          client,
		factory:         factory,
		metricsClient:   metricsClient,
		ctx:             ctx,
		cancel:          cancel,
		nodeCache:       make(map[string]*corev1.Node),
		podCache:        make(map[string]*corev1.Pod),
		deploymentCache: make(map[string]interface{}),
		serviceCache:    make(map[string]*corev1.Service),
		namespaceCache:  make(map[string]*corev1.Namespace),
		pvcCache:        make(map[string]*corev1.PersistentVolumeClaim),
	}

	// Initialize informers
	if err := im.setupInformers(); err != nil {
		cancel()
		return nil, err
	}

	return im, nil
}

// setupInformers configures all informers
func (im *InformerManager) setupInformers() error {
	// Node informer
	im.nodeInformer = im.factory.Core().V1().Nodes().Informer()
	im.nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    im.onNodeAdd,
		UpdateFunc: im.onNodeUpdate,
		DeleteFunc: im.onNodeDelete,
	})

	// Pod informer
	im.podInformer = im.factory.Core().V1().Pods().Informer()
	im.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    im.onPodAdd,
		UpdateFunc: im.onPodUpdate,
		DeleteFunc: im.onPodDelete,
	})

	// Deployment informer
	im.deploymentInformer = im.factory.Apps().V1().Deployments().Informer()
	im.deploymentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    im.onDeploymentAdd,
		UpdateFunc: im.onDeploymentUpdate,
		DeleteFunc: im.onDeploymentDelete,
	})

	// StatefulSet informer
	im.statefulSetInformer = im.factory.Apps().V1().StatefulSets().Informer()

	// Service informer
	im.serviceInformer = im.factory.Core().V1().Services().Informer()
	im.serviceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    im.onServiceAdd,
		UpdateFunc: im.onServiceUpdate,
		DeleteFunc: im.onServiceDelete,
	})

	// Namespace informer
	im.namespaceInformer = im.factory.Core().V1().Namespaces().Informer()
	im.namespaceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    im.onNamespaceAdd,
		UpdateFunc: im.onNamespaceUpdate,
		DeleteFunc: im.onNamespaceDelete,
	})

	// PVC informer
	im.pvcInformer = im.factory.Core().V1().PersistentVolumeClaims().Informer()

	// HPA informer
	im.hpaInformer = im.factory.Autoscaling().V2().HorizontalPodAutoscalers().Informer()

	// Job informer
	im.jobInformer = im.factory.Batch().V1().Jobs().Informer()

	return nil
}

// Start starts all informers
func (im *InformerManager) Start() error {
	im.mu.Lock()
	defer im.mu.Unlock()

	if im.started {
		return nil
	}

	log.Println("Starting Kubernetes informers...")

	// Start the shared informer factory
	im.factory.Start(im.ctx.Done())

	// Wait for all informers to sync
	log.Println("Waiting for informer caches to sync...")

	cacheSyncs := []cache.InformerSynced{
		im.nodeInformer.HasSynced,
		im.podInformer.HasSynced,
		im.deploymentInformer.HasSynced,
		im.serviceInformer.HasSynced,
		im.namespaceInformer.HasSynced,
		im.pvcInformer.HasSynced,
		im.hpaInformer.HasSynced,
		im.jobInformer.HasSynced,
	}

	// Wait for caches to sync with timeout
	waitCtx, waitCancel := context.WithTimeout(im.ctx, 2*time.Minute)
	defer waitCancel()

	if !cache.WaitForCacheSync(waitCtx.Done(), cacheSyncs...) {
		return fmt.Errorf("timed out waiting for informer caches to sync")
	}

	log.Println("Informer caches synced successfully")
	im.started = true

	// Start background tasks
	go im.periodicMetricsUpdate()

	return nil
}

// Stop stops all informers
func (im *InformerManager) Stop() {
	im.mu.Lock()
	defer im.mu.Unlock()

	if !im.started {
		return
	}

	log.Println("Stopping Kubernetes informers...")
	im.cancel()
	im.started = false
}

// AddNodeEventHandler adds an event handler for nodes
func (im *InformerManager) AddNodeEventHandler(handler cache.ResourceEventHandler) {
	im.mu.Lock()
	defer im.mu.Unlock()

	im.nodeHandlers = append(im.nodeHandlers, handler)
	if im.started {
		im.nodeInformer.AddEventHandler(handler)
	}
}

// AddPodEventHandler adds an event handler for pods
func (im *InformerManager) AddPodEventHandler(handler cache.ResourceEventHandler) {
	im.mu.Lock()
	defer im.mu.Unlock()

	im.podHandlers = append(im.podHandlers, handler)
	if im.started {
		im.podInformer.AddEventHandler(handler)
	}
}

// AddDeploymentEventHandler adds an event handler for deployments
func (im *InformerManager) AddDeploymentEventHandler(handler cache.ResourceEventHandler) {
	im.mu.Lock()
	defer im.mu.Unlock()

	im.deploymentHandlers = append(im.deploymentHandlers, handler)
	if im.started {
		im.deploymentInformer.AddEventHandler(handler)
	}
}

// Event handlers

func (im *InformerManager) onNodeAdd(obj interface{}) {
	node := obj.(*corev1.Node)
	im.cacheMu.Lock()
	im.nodeCache[node.Name] = node
	im.cacheMu.Unlock()

	log.Printf("Node added: %s", node.Name)
}

func (im *InformerManager) onNodeUpdate(oldObj, newObj interface{}) {
	oldNode := oldObj.(*corev1.Node)
	newNode := newObj.(*corev1.Node)

	im.cacheMu.Lock()
	im.nodeCache[newNode.Name] = newNode
	im.cacheMu.Unlock()

	// Log significant changes
	if oldNode.Status.Phase != newNode.Status.Phase {
		log.Printf("Node phase changed: %s (%s -> %s)",
			newNode.Name, oldNode.Status.Phase, newNode.Status.Phase)
	}
}

func (im *InformerManager) onNodeDelete(obj interface{}) {
	node := obj.(*corev1.Node)
	im.cacheMu.Lock()
	delete(im.nodeCache, node.Name)
	im.cacheMu.Unlock()

	log.Printf("Node deleted: %s", node.Name)
}

func (im *InformerManager) onPodAdd(obj interface{}) {
	pod := obj.(*corev1.Pod)
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	im.cacheMu.Lock()
	im.podCache[key] = pod
	im.cacheMu.Unlock()

	log.Printf("Pod added: %s", key)
}

func (im *InformerManager) onPodUpdate(oldObj, newObj interface{}) {
	oldPod := oldObj.(*corev1.Pod)
	newPod := newObj.(*corev1.Pod)
	key := fmt.Sprintf("%s/%s", newPod.Namespace, newPod.Name)

	im.cacheMu.Lock()
	im.podCache[key] = newPod
	im.cacheMu.Unlock()

	// Log significant changes
	if oldPod.Status.Phase != newPod.Status.Phase {
		log.Printf("Pod phase changed: %s (%s -> %s)",
			key, oldPod.Status.Phase, newPod.Status.Phase)
	}
}

func (im *InformerManager) onPodDelete(obj interface{}) {
	pod := obj.(*corev1.Pod)
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	im.cacheMu.Lock()
	delete(im.podCache, key)
	im.cacheMu.Unlock()

	log.Printf("Pod deleted: %s", key)
}

func (im *InformerManager) onDeploymentAdd(obj interface{}) {
	deployment := obj.(*appsv1.Deployment)
	key := fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)

	im.cacheMu.Lock()
	im.deploymentCache[key] = deployment
	im.cacheMu.Unlock()

	log.Printf("Deployment added: %s", key)
}

func (im *InformerManager) onDeploymentUpdate(oldObj, newObj interface{}) {
	oldDeployment := oldObj.(*appsv1.Deployment)
	newDeployment := newObj.(*appsv1.Deployment)
	key := fmt.Sprintf("%s/%s", newDeployment.Namespace, newDeployment.Name)

	im.cacheMu.Lock()
	im.deploymentCache[key] = newDeployment
	im.cacheMu.Unlock()

	// Log significant changes
	if oldDeployment.Status.Replicas != newDeployment.Status.Replicas {
		log.Printf("Deployment replica count changed: %s (%d -> %d)",
			key, oldDeployment.Status.Replicas, newDeployment.Status.Replicas)
	}
}

func (im *InformerManager) onDeploymentDelete(obj interface{}) {
	deployment := obj.(*appsv1.Deployment)
	key := fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)

	im.cacheMu.Lock()
	delete(im.deploymentCache, key)
	im.cacheMu.Unlock()

	log.Printf("Deployment deleted: %s", key)
}

func (im *InformerManager) onServiceAdd(obj interface{}) {
	service := obj.(*corev1.Service)
	key := fmt.Sprintf("%s/%s", service.Namespace, service.Name)

	im.cacheMu.Lock()
	im.serviceCache[key] = service
	im.cacheMu.Unlock()
}

func (im *InformerManager) onServiceUpdate(oldObj, newObj interface{}) {
	oldService := oldObj.(*corev1.Service)
	newService := newObj.(*corev1.Service)
	key := fmt.Sprintf("%s/%s", newService.Namespace, newService.Name)

	im.cacheMu.Lock()
	im.serviceCache[key] = newService
	im.cacheMu.Unlock()

	// Log significant changes
	if oldService.Spec.Type != newService.Spec.Type {
		log.Printf("Service type changed: %s (%s -> %s)",
			key, oldService.Spec.Type, newService.Spec.Type)
	}
}

func (im *InformerManager) onServiceDelete(obj interface{}) {
	service := obj.(*corev1.Service)
	key := fmt.Sprintf("%s/%s", service.Namespace, service.Name)

	im.cacheMu.Lock()
	delete(im.serviceCache, key)
	im.cacheMu.Unlock()
}

func (im *InformerManager) onNamespaceAdd(obj interface{}) {
	namespace := obj.(*corev1.Namespace)
	im.cacheMu.Lock()
	im.namespaceCache[namespace.Name] = namespace
	im.cacheMu.Unlock()

	log.Printf("Namespace added: %s", namespace.Name)
}

func (im *InformerManager) onNamespaceUpdate(oldObj, newObj interface{}) {
	newNamespace := newObj.(*corev1.Namespace)
	im.cacheMu.Lock()
	im.namespaceCache[newNamespace.Name] = newNamespace
	im.cacheMu.Unlock()
}

func (im *InformerManager) onNamespaceDelete(obj interface{}) {
	namespace := obj.(*corev1.Namespace)
	im.cacheMu.Lock()
	delete(im.namespaceCache, namespace.Name)
	im.cacheMu.Unlock()

	log.Printf("Namespace deleted: %s", namespace.Name)
}

// periodicMetricsUpdate updates metrics periodically
func (im *InformerManager) periodicMetricsUpdate() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			im.updateNodeMetrics()
			im.updatePodMetrics()
		case <-im.ctx.Done():
			return
		}
	}
}

// updateNodeMetrics updates node metrics from metrics-server
func (im *InformerManager) updateNodeMetrics() {
	nodeMetrics, err := im.metricsClient.MetricsV1beta1().NodeMetricses().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to get node metrics: %v", err)
		return
	}

	// Update metrics for each node
	im.cacheMu.Lock()
	defer im.cacheMu.Unlock()

	for _, metric := range nodeMetrics.Items {
		if node, ok := im.nodeCache[metric.Name]; ok {
			// You could add metrics to node annotations or a separate cache
			log.Printf("Updated metrics for node %s: CPU=%s, Memory=%s",
				metric.Name, metric.Usage.Cpu().String(), metric.Usage.Memory().String())
		}
	}
}

// updatePodMetrics updates pod metrics from metrics-server
func (im *InformerManager) updatePodMetrics() {
	podMetrics, err := im.metricsClient.MetricsV1beta1().PodMetricses("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to get pod metrics: %v", err)
		return
	}

	// Update metrics for each pod
	im.cacheMu.Lock()
	defer im.cacheMu.Unlock()

	for _, metric := range podMetrics.Items {
		key := fmt.Sprintf("%s/%s", metric.Namespace, metric.Name)
		if pod, ok := im.podCache[key]; ok {
			// You could add metrics to pod annotations or a separate cache
			// For now, just log the update
			log.Printf("Updated metrics for pod %s", key)
		}
	}
}

// GetNode returns a node from cache
func (im *InformerManager) GetNode(name string) (*corev1.Node, bool) {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	node, ok := im.nodeCache[name]
	return node, ok
}

// GetPod returns a pod from cache
func (im *InformerManager) GetPod(namespace, name string) (*corev1.Pod, bool) {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	pod, ok := im.podCache[key]
	return pod, ok
}

// GetDeployment returns a deployment from cache
func (im *InformerManager) GetDeployment(namespace, name string) (interface{}, bool) {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	deployment, ok := im.deploymentCache[key]
	return deployment, ok
}

// GetService returns a service from cache
func (im *InformerManager) GetService(namespace, name string) (*corev1.Service, bool) {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, name)
	service, ok := im.serviceCache[key]
	return service, ok
}

// ListNodes returns all nodes from cache
func (im *InformerManager) ListNodes() []*corev1.Node {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	nodes := make([]*corev1.Node, 0, len(im.nodeCache))
	for _, node := range im.nodeCache {
		nodes = append(nodes, node)
	}
	return nodes
}

// ListPods returns all pods from cache
func (im *InformerManager) ListPods() []*corev1.Pod {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	pods := make([]*corev1.Pod, 0, len(im.podCache))
	for _, pod := range im.podCache {
		pods = append(pods, pod)
	}
	return pods
}

// ListPodsByNamespace returns pods in a specific namespace
func (im *InformerManager) ListPodsByNamespace(namespace string) []*corev1.Pod {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	var pods []*corev1.Pod
	for _, pod := range im.podCache {
		if pod.Namespace == namespace {
			pods = append(pods, pod)
		}
	}
	return pods
}

// ListServices returns all services from cache
func (im *InformerManager) ListServices() []*corev1.Service {
	im.cacheMu.RLock()
	defer im.cacheMu.RUnlock()

	services := make([]*corev1.Service, 0, len(im.serviceCache))
	for _, service := range im.serviceCache {
		services = append(services, service)
	}
	return services
}

// WatchPod watches a specific pod for changes
func (im *InformerManager) WatchPod(namespace, name string, handler cache.ResourceEventHandler) error {
	// Create a filtered informer for the specific pod
	listWatch := cache.NewListWatchFromClient(
		im.client.clientset.CoreV1().RESTClient(),
		"pods",
		namespace,
		fields.OneTermEqualSelector("metadata.name", name),
	)

	_, controller := cache.NewIndexerInformer(
		listWatch,
		&corev1.Pod{},
		0,
		handler,
		cache.Indexers{},
	)

	go controller.Run(im.ctx.Done())
	return nil
}
