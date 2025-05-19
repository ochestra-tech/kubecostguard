// 3. Health Monitoring Implementation
// File: internal/health/monitor.go
package health

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/ochestra-tech/kubecostguard/internal/config"
	"github.com/ochestra-tech/kubecostguard/internal/health/alerting"
	"github.com/ochestra-tech/kubecostguard/internal/health/collectors"
	"github.com/ochestra-tech/kubecostguard/internal/kubernetes"
)

// Monitor coordinates health monitoring of the Kubernetes cluster
type Monitor struct {
	ctx          context.Context
	k8sClient    *kubernetes.Client
	config       config.HealthConfig
	collectors   []collectors.Collector
	alertManager *alerting.AlertManager
	metrics      map[string]interface{}
	metricsMu    sync.RWMutex
	stopChan     chan struct{}
}

// NewMonitor creates a new health monitor
func NewMonitor(ctx context.Context, k8sClient *kubernetes.Client, config config.HealthConfig) *Monitor {
	m := &Monitor{
		ctx:       ctx,
		k8sClient: k8sClient,
		config:    config,
		metrics:   make(map[string]interface{}),
		stopChan:  make(chan struct{}),
	}

	// Initialize collectors based on configuration
	m.initCollectors()

	// Initialize alert manager
	m.alertManager = alerting.NewAlertManager(config.AlertThresholds)

	return m
}

// initCollectors sets up the enabled metric collectors
func (m *Monitor) initCollectors() {
	for _, collectorName := range m.config.EnabledCollectors {
		switch collectorName {
		case "node":
			m.collectors = append(m.collectors, collectors.NewNodeCollector(m.k8sClient))
		case "pod":
			m.collectors = append(m.collectors, collectors.NewPodCollector(m.k8sClient))
		case "controlplane":
			m.collectors = append(m.collectors, collectors.NewControlPlaneCollector(m.k8sClient))
		default:
			log.Printf("Unknown collector: %s", collectorName)
		}
	}
}

// Start begins the health monitoring process
func (m *Monitor) Start() error {
	// Start collectors
	for _, collector := range m.collectors {
		if err := collector.Start(m.ctx); err != nil {
			return err
		}
	}

	// Start metrics collection loop
	go m.collectLoop()

	return nil
}

// collectLoop periodically collects metrics from all collectors
func (m *Monitor) collectLoop() {
	ticker := time.NewTicker(time.Duration(m.config.ScrapeIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.collectMetrics()
		case <-m.stopChan:
			return
		case <-m.ctx.Done():
			return
		}
	}
}

// collectMetrics gathers metrics from all collectors
func (m *Monitor) collectMetrics() {
	for _, collector := range m.collectors {
		metrics, err := collector.Collect()
		if err != nil {
			log.Printf("Error collecting metrics from %s: %v", collector.Name(), err)
			continue
		}

		// Update metrics store
		m.metricsMu.Lock()
		for k, v := range metrics {
			m.metrics[k] = v
		}
		m.metricsMu.Unlock()

		// Check for alerts
		m.alertManager.CheckMetrics(metrics)
	}
}

// GetMetrics returns a copy of the current metrics
func (m *Monitor) GetMetrics() map[string]interface{} {
	m.metricsMu.RLock()
	defer m.metricsMu.RUnlock()

	// Create a copy of the metrics
	result := make(map[string]interface{}, len(m.metrics))
	for k, v := range m.metrics {
		result[k] = v
	}

	return result
}

// Stop halts the health monitoring process
func (m *Monitor) Stop() {
	close(m.stopChan)

	// Stop individual collectors
	for _, collector := range m.collectors {
		if err := collector.Stop(); err != nil {
			log.Printf("Error stopping collector %s: %v", collector.Name(), err)
		}
	}
}
