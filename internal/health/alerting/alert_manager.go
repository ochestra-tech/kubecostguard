// File: internal/health/alerting/alert_manager.go
package alerting

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Severity levels for alerts
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// AlertStatus represents the current status of an alert
type AlertStatus string

const (
	AlertStatusActive   AlertStatus = "active"
	AlertStatusResolved AlertStatus = "resolved"
	AlertStatusSilenced AlertStatus = "silenced"
	AlertStatusMuted    AlertStatus = "muted"
)

// Alert represents a health alert
type Alert struct {
	ID                string
	Severity          Severity
	Status            AlertStatus
	ResourceType      string // node, pod, control-plane
	ResourceName      string
	Message           string
	Details           map[string]interface{}
	StartTime         time.Time
	LastOccurrence    time.Time
	ResolvedTime      *time.Time
	NotificationCount int
	Silenced          bool
	SilenceUntil      *time.Time
	RepeatInterval    time.Duration
}

// AlertRule defines conditions for triggering alerts
type AlertRule struct {
	Name                 string
	MetricPath           string // e.g., "node_health.cpu_utilization_percent"
	Condition            string // "gt", "lt", "eq"
	Threshold            float64
	Duration             time.Duration // How long condition must persist
	Severity             Severity
	Message              string
	Enabled              bool
	RepeatInterval       time.Duration
	NotificationChannels []string
}

// AlertThresholds contains the default threshold configurations
type AlertThresholds struct {
	CPUUtilizationPercent    float64
	MemoryUtilizationPercent float64
	PodRestarts              int
	DiskPressure             bool
	MemoryPressure           bool
	NodeNotReady             time.Duration
	PodPendingTimeout        time.Duration
}

// NotificationChannel interface for alert notifications
type NotificationChannel interface {
	Name() string
	Send(ctx context.Context, alert *Alert) error
	Test(ctx context.Context) error
}

// AlertManager manages health alerts and notifications
type AlertManager struct {
	alerts          map[string]*Alert
	alertHistory    []Alert
	rules           map[string]*AlertRule
	thresholds      AlertThresholds
	channels        map[string]NotificationChannel
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	maxHistorySize  int
	cleanupInterval time.Duration
}

// NewAlertManager creates a new alert manager
func NewAlertManager(thresholds AlertThresholds) *AlertManager {
	ctx, cancel := context.WithCancel(context.Background())

	am := &AlertManager{
		alerts:          make(map[string]*Alert),
		alertHistory:    make([]Alert, 0),
		rules:           make(map[string]*AlertRule),
		thresholds:      thresholds,
		channels:        make(map[string]NotificationChannel),
		ctx:             ctx,
		cancel:          cancel,
		maxHistorySize:  1000,
		cleanupInterval: 24 * time.Hour,
	}

	// Set up default rules
	am.setupDefaultRules()

	// Start cleanup goroutine
	go am.cleanupRoutine()

	return am
}

// setupDefaultRules creates the default alert rules
func (am *AlertManager) setupDefaultRules() {
	defaultRules := []AlertRule{
		{
			Name:           "HighCPUUtilization",
			MetricPath:     "cpu_utilization_percent",
			Condition:      "gt",
			Threshold:      am.thresholds.CPUUtilizationPercent,
			Duration:       5 * time.Minute,
			Severity:       SeverityCritical,
			Message:        "High CPU utilization detected",
			Enabled:        true,
			RepeatInterval: 30 * time.Minute,
		},
		{
			Name:           "HighMemoryUtilization",
			MetricPath:     "memory_utilization_percent",
			Condition:      "gt",
			Threshold:      am.thresholds.MemoryUtilizationPercent,
			Duration:       5 * time.Minute,
			Severity:       SeverityCritical,
			Message:        "High memory utilization detected",
			Enabled:        true,
			RepeatInterval: 30 * time.Minute,
		},
		{
			Name:           "PodRestarts",
			MetricPath:     "pod_restarts",
			Condition:      "gt",
			Threshold:      float64(am.thresholds.PodRestarts),
			Duration:       1 * time.Minute,
			Severity:       SeverityWarning,
			Message:        "Pod restart count threshold exceeded",
			Enabled:        true,
			RepeatInterval: 60 * time.Minute,
		},
		{
			Name:           "NodeNotReady",
			MetricPath:     "node_status",
			Condition:      "eq",
			Threshold:      0, // 0 = NotReady state
			Duration:       am.thresholds.NodeNotReady,
			Severity:       SeverityCritical,
			Message:        "Node is not ready",
			Enabled:        true,
			RepeatInterval: 15 * time.Minute,
		},
		{
			Name:           "PodPendingLong",
			MetricPath:     "pod_pending_duration",
			Condition:      "gt",
			Threshold:      am.thresholds.PodPendingTimeout.Minutes(),
			Duration:       1 * time.Minute,
			Severity:       SeverityWarning,
			Message:        "Pod has been pending for too long",
			Enabled:        true,
			RepeatInterval: 30 * time.Minute,
		},
		{
			Name:           "ControlPlaneUnhealthy",
			MetricPath:     "control_plane_status",
			Condition:      "eq",
			Threshold:      0, // 0 = Unhealthy state
			Duration:       1 * time.Minute,
			Severity:       SeverityCritical,
			Message:        "Control plane health is degraded",
			Enabled:        true,
			RepeatInterval: 10 * time.Minute,
		},
	}

	for _, rule := range defaultRules {
		am.AddRule(rule)
	}
}

// AddRule adds a new alert rule
func (am *AlertManager) AddRule(rule AlertRule) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.rules[rule.Name] = &rule
}

// RemoveRule removes an alert rule
func (am *AlertManager) RemoveRule(ruleName string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	delete(am.rules, ruleName)
}

// AddChannel adds a notification channel
func (am *AlertManager) AddChannel(channel NotificationChannel) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.channels[channel.Name()] = channel
}

// CheckMetrics evaluates metrics against alert rules
func (am *AlertManager) CheckMetrics(metrics map[string]interface{}) {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Evaluate each rule against the metrics
	for _, rule := range am.rules {
		if !rule.Enabled {
			continue
		}

		// Extract value from metrics using the rule's metric path
		value := am.extractMetricValue(metrics, rule.MetricPath)
		if value == nil {
			continue
		}

		// Evaluate the condition
		triggered := am.evaluateCondition(rule, value)

		// Process the rule evaluation
		am.processRuleEvaluation(rule, triggered, metrics)
	}
}

// extractMetricValue extracts a value from nested metrics map
func (am *AlertManager) extractMetricValue(metrics map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	current := metrics

	for i, part := range parts {
		if i == len(parts)-1 {
			return current[part]
		}

		if nextMap, ok := current[part].(map[string]interface{}); ok {
			current = nextMap
		} else {
			return nil
		}
	}

	return nil
}

// evaluateCondition checks if a value meets the rule condition
func (am *AlertManager) evaluateCondition(rule *AlertRule, value interface{}) bool {
	floatValue, ok := am.toFloat64(value)
	if !ok {
		return false
	}

	switch rule.Condition {
	case "gt":
		return floatValue > rule.Threshold
	case "lt":
		return floatValue < rule.Threshold
	case "eq":
		return floatValue == rule.Threshold
	case "ne":
		return floatValue != rule.Threshold
	case "ge":
		return floatValue >= rule.Threshold
	case "le":
		return floatValue <= rule.Threshold
	default:
		return false
	}
}

// toFloat64 converts various types to float64
func (am *AlertManager) toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		if v == "NotReady" {
			return 0, true // 0 = NotReady for conditions
		}
		if v == "Unhealthy" {
			return 0, true // 0 = Unhealthy for control plane
		}
		if v == "Ready" || v == "Healthy" {
			return 1, true // 1 = Healthy/Ready states
		}
	}
	return 0, false
}

// processRuleEvaluation handles the evaluation result
func (am *AlertManager) processRuleEvaluation(rule *AlertRule, triggered bool, metrics map[string]interface{}) {
	alertID := am.generateAlertID(rule, metrics)

	if triggered {
		alert, exists := am.alerts[alertID]
		if !exists {
			// Create new alert
			alert = &Alert{
				ID:             alertID,
				Severity:       rule.Severity,
				Status:         AlertStatusActive,
				ResourceType:   am.extractResourceType(metrics),
				ResourceName:   am.extractResourceName(metrics),
				Message:        rule.Message,
				Details:        am.extractAlertDetails(metrics),
				StartTime:      time.Now(),
				LastOccurrence: time.Now(),
				RepeatInterval: rule.RepeatInterval,
			}
			am.alerts[alertID] = alert
			am.notifyAlert(alert)
		} else {
			// Update existing alert
			alert.LastOccurrence = time.Now()
			alert.Status = AlertStatusActive
			alert.Details = am.extractAlertDetails(metrics)

			// Check if we should resend notification
			if time.Since(alert.StartTime) > alert.RepeatInterval {
				alert.NotificationCount++
				am.notifyAlert(alert)
			}
		}
	} else {
		// Check if alert exists and should be resolved
		if alert, exists := am.alerts[alertID]; exists {
			if alert.Status == AlertStatusActive {
				resolvedTime := time.Now()
				alert.Status = AlertStatusResolved
				alert.ResolvedTime = &resolvedTime

				// Notify resolution
				am.notifyAlertResolution(alert)

				// Move to history
				am.addToHistory(*alert)
				delete(am.alerts, alertID)
			}
		}
	}
}

// notifyAlert sends alert notifications through all channels
func (am *AlertManager) notifyAlert(alert *Alert) {
	if alert.Silenced {
		return
	}

	for _, channel := range am.channels {
		go func(ch NotificationChannel) {
			if err := ch.Send(am.ctx, alert); err != nil {
				log.Printf("Failed to send alert via %s: %v", ch.Name(), err)
			}
		}(channel)
	}
}

// notifyAlertResolution sends resolution notifications
func (am *AlertManager) notifyAlertResolution(alert *Alert) {
	resolvedAlert := *alert
	resolvedAlert.Message = fmt.Sprintf("RESOLVED: %s", alert.Message)

	for _, channel := range am.channels {
		go func(ch NotificationChannel) {
			if err := ch.Send(am.ctx, &resolvedAlert); err != nil {
				log.Printf("Failed to send resolution via %s: %v", ch.Name(), err)
			}
		}(channel)
	}
}

// generateAlertID creates a unique identifier for alerts
func (am *AlertManager) generateAlertID(rule *AlertRule, metrics map[string]interface{}) string {
	resource := am.extractResourceName(metrics)
	return fmt.Sprintf("%s_%s_%s", rule.Name, am.extractResourceType(metrics), resource)
}

// extractResourceType extracts the resource type from metrics
func (am *AlertManager) extractResourceType(metrics map[string]interface{}) string {
	if _, ok := metrics["node_health_summary"]; ok {
		return "node"
	}
	if _, ok := metrics["pod_health_summary"]; ok {
		return "pod"
	}
	if _, ok := metrics["control_plane_status"]; ok {
		return "control-plane"
	}
	return "unknown"
}

// extractResourceName extracts the resource name from metrics
func (am *AlertManager) extractResourceName(metrics map[string]interface{}) string {
	// This would need to be more sophisticated in practice
	// For now, return a simplified identifier
	if nodeDetails, ok := metrics["node_details"].(map[string]interface{}); ok {
		for name := range nodeDetails {
			return name // Return first node name found
		}
	}
	return "unknown"
}

// extractAlertDetails extracts relevant details for an alert
func (am *AlertManager) extractAlertDetails(metrics map[string]interface{}) map[string]interface{} {
	details := make(map[string]interface{})

	// Extract relevant metric values
	if cpu, ok := metrics["cpu_utilization_percent"].(float64); ok {
		details["cpu_utilization_percent"] = cpu
	}
	if memory, ok := metrics["memory_utilization_percent"].(float64); ok {
		details["memory_utilization_percent"] = memory
	}
	if status, ok := metrics["node_status"].(string); ok {
		details["node_status"] = status
	}

	return details
}

// addToHistory adds an alert to the history
func (am *AlertManager) addToHistory(alert Alert) {
	am.alertHistory = append(am.alertHistory, alert)

	// Trim history if too large
	if len(am.alertHistory) > am.maxHistorySize {
		am.alertHistory = am.alertHistory[len(am.alertHistory)-am.maxHistorySize:]
	}
}

// cleanupRoutine periodically cleans up old alerts and history
func (am *AlertManager) cleanupRoutine() {
	ticker := time.NewTicker(am.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			am.cleanup()
		case <-am.ctx.Done():
			return
		}
	}
}

// cleanup removes old resolved alerts and trims history
func (am *AlertManager) cleanup() {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Remove resolved alerts older than 7 days
	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour)
	for id, alert := range am.alerts {
		if alert.Status == AlertStatusResolved && alert.ResolvedTime != nil &&
			alert.ResolvedTime.Before(sevenDaysAgo) {
			delete(am.alerts, id)
		}
	}

	// Remove history older than 30 days
	thirtyDaysAgo := time.Now().Add(-30 * 24 * time.Hour)
	newHistory := make([]Alert, 0, len(am.alertHistory))
	for _, alert := range am.alertHistory {
		if alert.StartTime.After(thirtyDaysAgo) {
			newHistory = append(newHistory, alert)
		}
	}
	am.alertHistory = newHistory
}

// GetActiveAlerts returns all active alerts
func (am *AlertManager) GetActiveAlerts() map[string]*Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	active := make(map[string]*Alert)
	for id, alert := range am.alerts {
		if alert.Status == AlertStatusActive {
			alertCopy := *alert
			active[id] = &alertCopy
		}
	}
	return active
}

// GetAlertHistory returns alert history
func (am *AlertManager) GetAlertHistory(limit int) []Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	if limit <= 0 || limit > len(am.alertHistory) {
		limit = len(am.alertHistory)
	}

	history := make([]Alert, limit)
	copy(history, am.alertHistory[len(am.alertHistory)-limit:])
	return history
}

// SilenceAlert silences an active alert
func (am *AlertManager) SilenceAlert(alertID string, duration time.Duration) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	alert, exists := am.alerts[alertID]
	if !exists {
		return fmt.Errorf("alert not found: %s", alertID)
	}

	silenceUntil := time.Now().Add(duration)
	alert.Silenced = true
	alert.SilenceUntil = &silenceUntil
	alert.Status = AlertStatusSilenced

	return nil
}

// UnsilenceAlert removes silence from an alert
func (am *AlertManager) UnsilenceAlert(alertID string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	alert, exists := am.alerts[alertID]
	if !exists {
		return fmt.Errorf("alert not found: %s", alertID)
	}

	alert.Silenced = false
	alert.SilenceUntil = nil
	alert.Status = AlertStatusActive

	return nil
}

// Stop stops the alert manager
func (am *AlertManager) Stop() {
	am.cancel()
}
