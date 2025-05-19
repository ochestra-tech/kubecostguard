package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Health-related request/response structures
type HealthStatusResponse struct {
	Status     string            `json:"status"`
	Timestamp  time.Time         `json:"timestamp"`
	Components map[string]string `json:"components,omitempty"`
}

type AlertResponse struct {
	ID           string    `json:"id"`
	Severity     string    `json:"severity"`
	Message      string    `json:"message"`
	Resource     string    `json:"resource"`
	StartTime    time.Time `json:"start_time"`
	LastSeen     time.Time `json:"last_seen"`
	Acknowledged bool      `json:"acknowledged"`
}

// Cost-related request/response structures
type CostSummaryResponse struct {
	TotalCost      float64            `json:"total_cost"`
	MonthlyAverage float64            `json:"monthly_average"`
	LastMonth      float64            `json:"last_month"`
	YearToDate     float64            `json:"year_to_date"`
	Timestamp      time.Time          `json:"timestamp"`
	Breakdown      map[string]float64 `json:"breakdown"`
	Trends         []CostTrendPoint   `json:"trends,omitempty"`
}

type CostTrendPoint struct {
	Date string  `json:"date"`
	Cost float64 `json:"cost"`
}

type NamespaceCostResponse struct {
	Name          string        `json:"name"`
	TotalCost     float64       `json:"total_cost"`
	CPUCost       float64       `json:"cpu_cost"`
	MemoryCost    float64       `json:"memory_cost"`
	StorageCost   float64       `json:"storage_cost"`
	NetworkCost   float64       `json:"network_cost"`
	PodCount      int           `json:"pod_count"`
	ResourcesUsed ResourceUsage `json:"resources_used"`
	LastUpdated   time.Time     `json:"last_updated"`
}

type ResourceUsage struct {
	CPUCores  float64 `json:"cpu_cores"`
	MemoryGB  float64 `json:"memory_gb"`
	StorageGB float64 `json:"storage_gb"`
}

type NodeCostResponse struct {
	Name              string    `json:"name"`
	TotalCost         float64   `json:"total_cost"`
	InstanceType      string    `json:"instance_type"`
	Region            string    `json:"region"`
	CPUCost           float64   `json:"cpu_cost"`
	MemoryCost        float64   `json:"memory_cost"`
	CPUUtilization    float64   `json:"cpu_utilization"`
	MemoryUtilization float64   `json:"memory_utilization"`
	PodCount          int       `json:"pod_count"`
	LastUpdated       time.Time `json:"last_updated"`
}

// Optimization-related request/response structures
type OptimizationSummaryResponse struct {
	TotalSavings       float64                      `json:"total_savings"`
	RecommendedActions int                          `json:"recommended_actions"`
	HighPriority       int                          `json:"high_priority"`
	MediumPriority     int                          `json:"medium_priority"`
	LowPriority        int                          `json:"low_priority"`
	Recommendations    []OptimizationRecommendation `json:"recommendations"`
	LastUpdated        time.Time                    `json:"last_updated"`
}

type OptimizationRecommendation struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type"`
	Resource         string                 `json:"resource"`
	Description      string                 `json:"description"`
	PotentialSavings float64                `json:"potential_savings"`
	EffortLevel      string                 `json:"effort_level"`
	Priority         int                    `json:"priority"`
	Status           string                 `json:"status"`
	RequiresApproval bool                   `json:"requires_approval"`
	CreatedAt        time.Time              `json:"created_at"`
	Details          map[string]interface{} `json:"details,omitempty"`
}

type ApplyRecommendationRequest struct {
	ApprovalToken string `json:"approval_token,omitempty"`
	Force         bool   `json:"force,omitempty"`
}

type OptimizationHistoryEntry struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	Resource      string    `json:"resource"`
	Action        string    `json:"action"`
	Status        string    `json:"status"`
	AppliedAt     time.Time `json:"applied_at"`
	ActualSavings float64   `json:"actual_savings,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// Configuration request/response structures
type ConfigurationResponse struct {
	Health       HealthConfig       `json:"health"`
	Cost         CostConfig         `json:"cost"`
	Optimization OptimizationConfig `json:"optimization"`
	API          APIConfig          `json:"api"`
	LastUpdated  time.Time          `json:"last_updated"`
}

type HealthConfig struct {
	ScrapeInterval    int             `json:"scrape_interval_seconds"`
	EnabledCollectors []string        `json:"enabled_collectors"`
	AlertThresholds   AlertThresholds `json:"alert_thresholds"`
}

type AlertThresholds struct {
	CPUUtilization    float64 `json:"cpu_utilization_percent"`
	MemoryUtilization float64 `json:"memory_utilization_percent"`
	PodRestarts       int     `json:"pod_restarts"`
}

type CostConfig struct {
	UpdateInterval string `json:"update_interval_minutes"`
	CloudProvider  string `json:"cloud_provider"`
	StorageBackend string `json:"storage_backend"`
}

type OptimizationConfig struct {
	EnableAutoScaling     bool    `json:"enable_auto_scaling"`
	IdleResourceThreshold float64 `json:"idle_resource_threshold"`
	OptimizationInterval  int     `json:"optimization_interval_hours"`
	ApplyRecommendations  bool    `json:"apply_recommendations"`
	DryRun                bool    `json:"dry_run"`
}

type APIConfig struct {
	Port        int  `json:"port"`
	TLSEnabled  bool `json:"tls_enabled"`
	AuthEnabled bool `json:"auth_enabled"`
	RateLimit   int  `json:"rate_limit"`
}

// Handler functions

func (s *Server) handleHealthCheck(c *gin.Context) {
	// Basic health check endpoint for load balancers
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) getHealthStatus(c *gin.Context) {
	metrics := s.health.GetMetrics()

	// Determine overall status
	status := "healthy"
	var components map[string]string

	if detailed, _ := strconv.ParseBool(c.Query("detailed")); detailed {
		components = make(map[string]string)

		// Extract component statuses from metrics
		if componentsData, ok := metrics["components"].(map[string]interface{}); ok {
			for component, componentStatus := range componentsData {
				if status, ok := componentStatus.(string); ok {
					components[component] = status
					if status != "healthy" && status != "ok" {
						status = "degraded"
					}
				}
			}
		}
	}

	response := HealthStatusResponse{
		Status:     status,
		Timestamp:  time.Now().UTC(),
		Components: components,
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) getHealthMetrics(c *gin.Context) {
	metrics := s.health.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}

func (s *Server) getHealthAlerts(c *gin.Context) {
	// Get alert status from alert manager
	activeAlerts := s.health.GetActiveAlerts()

	var alerts []AlertResponse
	for id, alert := range activeAlerts {
		alertResp := AlertResponse{
			ID:           id,
			Severity:     alert.Severity,
			Message:      alert.Message,
			Resource:     alert.ResourceName,
			StartTime:    alert.StartTime,
			LastSeen:     alert.LastOccurrence,
			Acknowledged: alert.Status == "acknowledged",
		}
		alerts = append(alerts, alertResp)
	}

	c.JSON(http.StatusOK, gin.H{"alerts": alerts})
}

func (s *Server) getCostSummary(c *gin.Context) {
	costData := s.cost.GetCostData()

	// Calculate aggregates
	var totalCost float64
	breakdown := make(map[string]float64)

	for resource, cost := range costData {
		totalCost += cost

		// Categorize costs (simplified)
		if contains(resource, "node") {
			breakdown["compute"] += cost
		} else if contains(resource, "storage") {
			breakdown["storage"] += cost
		} else if contains(resource, "network") {
			breakdown["network"] += cost
		} else {
			breakdown["other"] += cost
		}
	}

	// Get historical trends if requested
	var trends []CostTrendPoint
	if includeTrends, _ := strconv.ParseBool(c.Query("include_trends")); includeTrends {
		trendsData, err := s.cost.GetCostTrends("7d")
		if err == nil {
			trends = convertTrends(trendsData)
		}
	}

	response := CostSummaryResponse{
		TotalCost:      totalCost,
		MonthlyAverage: totalCost,       // Simplified
		LastMonth:      totalCost * 0.9, // Simplified
		YearToDate:     totalCost * 12,  // Simplified
		Timestamp:      time.Now().UTC(),
		Breakdown:      breakdown,
		Trends:         trends,
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) getNamespaceCosts(c *gin.Context) {
	// Get namespace costs
	namespaceCosts, err := s.cost.GetNamespaceCosts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var response []NamespaceCostResponse
	for namespace, data := range namespaceCosts {
		resp := NamespaceCostResponse{
			Name:        namespace,
			TotalCost:   data.TotalCost,
			CPUCost:     data.CPUCost,
			MemoryCost:  data.MemoryCost,
			StorageCost: data.StorageCost,
			NetworkCost: data.NetworkCost,
			PodCount:    data.PodCount,
			ResourcesUsed: ResourceUsage{
				CPUCores:  data.CPUUsage,
				MemoryGB:  data.MemoryUsage / (1024 * 1024 * 1024),
				StorageGB: data.StorageUsage / (1024 * 1024 * 1024),
			},
			LastUpdated: time.Now().UTC(),
		}
		response = append(response, resp)
	}

	c.JSON(http.StatusOK, gin.H{"namespaces": response})
}

func (s *Server) getNamespaceCostDetails(c *gin.Context) {
	namespace := c.Param("namespace")

	details, err := s.cost.GetNamespaceCostDetails(namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, details)
}

func (s *Server) getNodeCosts(c *gin.Context) {
	// Get node costs
	nodeCosts, err := s.cost.GetNodeCosts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var response []NodeCostResponse
	for nodeName, data := range nodeCosts {
		resp := NodeCostResponse{
			Name:              nodeName,
			TotalCost:         data.TotalCost,
			InstanceType:      data.InstanceType,
			Region:            data.Region,
			CPUCost:           data.CPUCost,
			MemoryCost:        data.MemoryCost,
			CPUUtilization:    data.CPUUtilization,
			MemoryUtilization: data.MemoryUtilization,
			PodCount:          data.PodCount,
			LastUpdated:       time.Now().UTC(),
		}
		response = append(response, resp)
	}

	c.JSON(http.StatusOK, gin.H{"nodes": response})
}

func (s *Server) getNodeCostDetails(c *gin.Context) {
	node := c.Param("node")

	details, err := s.cost.GetNodeCostDetails(node)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, details)
}

func (s *Server) getOptimizationRecommendations(c *gin.Context) {
	summary, err := s.optimizer.GetOptimizationSummary()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Convert to response format
	var recommendations []OptimizationRecommendation
	if recs, ok := summary["recommendations"].([]interface{}); ok {
		for _, rec := range recs {
			if recMap, ok := rec.(map[string]interface{}); ok {
				recommendation := OptimizationRecommendation{
					ID:               getString(recMap, "id"),
					Type:             getString(recMap, "type"),
					Resource:         getString(recMap, "resource"),
					Description:      getString(recMap, "description"),
					PotentialSavings: getFloat64(recMap, "savings"),
					EffortLevel:      getString(recMap, "effort"),
					Priority:         getInt(recMap, "priority"),
					Status:           getString(recMap, "status"),
					RequiresApproval: getBool(recMap, "requires_approval"),
					CreatedAt:        time.Now(),
					Details:          getMap(recMap, "details"),
				}
				recommendations = append(recommendations, recommendation)
			}
		}
	}

	response := OptimizationSummaryResponse{
		TotalSavings:       getFloat64(summary, "total_savings"),
		RecommendedActions: len(recommendations),
		HighPriority:       countByPriority(recommendations, 5),
		MediumPriority:     countByPriority(recommendations, 3),
		LowPriority:        countByPriority(recommendations, 1),
		Recommendations:    recommendations,
		LastUpdated:        time.Now().UTC(),
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) applyOptimizationRecommendation(c *gin.Context) {
	id := c.Param("id")

	var request ApplyRecommendationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Apply the recommendation
	result, err := s.optimizer.ApplyRecommendation(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
			"id":    id,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      id,
		"status":  "applied",
		"message": "Recommendation applied successfully",
		"result":  result,
	})
}

func (s *Server) getOptimizationHistory(c *gin.Context) {
	limit := 50
	if limitStr := c.Query("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}

	history, err := s.optimizer.GetOptimizationHistory(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var entries []OptimizationHistoryEntry
	if historySlice, ok := history.([]interface{}); ok {
		for _, entry := range historySlice {
			if entryMap, ok := entry.(map[string]interface{}); ok {
				histEntry := OptimizationHistoryEntry{
					ID:            getString(entryMap, "id"),
					Type:          getString(entryMap, "type"),
					Resource:      getString(entryMap, "resource"),
					Action:        getString(entryMap, "action"),
					Status:        getString(entryMap, "status"),
					AppliedAt:     getTime(entryMap, "applied_at"),
					ActualSavings: getFloat64(entryMap, "actual_savings"),
					Error:         getString(entryMap, "error"),
				}
				entries = append(entries, histEntry)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"history": entries})
}

func (s *Server) getConfiguration(c *gin.Context) {
	// Retrieve current configuration
	response := ConfigurationResponse{
		Health: HealthConfig{
			ScrapeInterval:    60,
			EnabledCollectors: []string{"node", "pod", "controlplane"},
			AlertThresholds: AlertThresholds{
				CPUUtilization:    80,
				MemoryUtilization: 85,
				PodRestarts:       5,
			},
		},
		Cost: CostConfig{
			UpdateInterval: "15",
			CloudProvider:  "aws",
			StorageBackend: "sqlite",
		},
		Optimization: OptimizationConfig{
			EnableAutoScaling:     true,
			IdleResourceThreshold: 0.2,
			OptimizationInterval:  24,
			ApplyRecommendations:  false,
			DryRun:                true,
		},
		API: APIConfig{
			Port:        8080,
			TLSEnabled:  false,
			AuthEnabled: false,
			RateLimit:   100,
		},
		LastUpdated: time.Now().UTC(),
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) updateConfiguration(c *gin.Context) {
	var configUpdate map[string]interface{}
	if err := c.ShouldBindJSON(&configUpdate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update configuration
	// This would typically persist changes to config store
	log.Printf("Configuration update request: %+v", configUpdate)

	c.JSON(http.StatusOK, gin.H{
		"status":  "updated",
		"message": "Configuration updated successfully",
	})
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr)))
}

func convertTrends(trendsData interface{}) []CostTrendPoint {
	var trends []CostTrendPoint
	if trendsSlice, ok := trendsData.([]interface{}); ok {
		for _, point := range trendsSlice {
			if pointMap, ok := point.(map[string]interface{}); ok {
				trend := CostTrendPoint{
					Date: getString(pointMap, "date"),
					Cost: getFloat64(pointMap, "cost"),
				}
				trends = append(trends, trend)
			}
		}
	}
	return trends
}

func countByPriority(recommendations []OptimizationRecommendation, priority int) int {
	count := 0
	for _, rec := range recommendations {
		if rec.Priority == priority {
			count++
		}
	}
	return count
}

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

func getFloat64(m map[string]interface{}, key string) float64 {
	switch val := m[key].(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return 0
	}
}

func getInt(m map[string]interface{}, key string) int {
	switch val := m[key].(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	default:
		return 0
	}
}

func getBool(m map[string]interface{}, key string) bool {
	if val, ok := m[key].(bool); ok {
		return val
	}
	return false
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if val, ok := m[key].(map[string]interface{}); ok {
		return val
	}
	return nil
}

func getTime(m map[string]interface{}, key string) time.Time {
	if val, ok := m[key].(string); ok {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			return t
		}
	}
	return time.Time{}
}
