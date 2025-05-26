package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ochestra-tech/kubecostguard/internal/config"
	"github.com/ochestra-tech/kubecostguard/internal/cost"
	"github.com/ochestra-tech/kubecostguard/internal/health"
	"github.com/ochestra-tech/kubecostguard/internal/optimization"
)

// NamespaceCostData represents cost data for a namespace
type NamespaceCostData struct {
	Name        string  `json:"name"`
	TotalCost   float64 `json:"total_cost"`
	CPUCost     float64 `json:"cpu_cost"`
	MemoryCost  float64 `json:"memory_cost"`
	StorageCost float64 `json:"storage_cost"`
	PodCount    int     `json:"pod_count"`
}

// NodeCostData represents cost data for a node
type NodeCostData struct {
	Name              string  `json:"name"`
	TotalCost         float64 `json:"total_cost"`
	CPUCost           float64 `json:"cpu_cost"`
	MemoryCost        float64 `json:"memory_cost"`
	InstanceType      string  `json:"instance_type"`
	CPUUtilization    float64 `json:"cpu_utilization"`
	MemoryUtilization float64 `json:"memory_utilization"`
}

// Server handles the HTTP API for the application
type Server struct {
	router     *gin.Engine
	config     config.APIConfig
	health     *health.Monitor
	cost       *cost.Analyzer
	optimizer  *optimization.Optimizer
	httpServer *http.Server
}

// NewServer creates a new API server
func NewServer(config config.APIConfig, health *health.Monitor, cost *cost.Analyzer, optimizer *optimization.Optimizer) *Server {
	s := &Server{
		config:    config,
		health:    health,
		cost:      cost,
		optimizer: optimizer,
	}

	// Initialize router
	s.setupRouter()

	return s
}

// setupRouter configures the HTTP router
func (s *Server) setupRouter() {
	// Create router with recovery middleware
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.loggingMiddleware())

	// Add middleware
	if s.config.Authentication.Enabled {
		r.Use(s.authMiddleware())
	}
	r.Use(corsMiddleware())

	// API routes
	api := r.Group("/api")
	{
		// Health endpoints
		health := api.Group("/health")
		{
			health.GET("/status", s.getHealthStatus)
			health.GET("/metrics", s.getHealthMetrics)
			health.GET("/alerts", s.getHealthAlerts)
		}

		// Cost endpoints
		cost := api.Group("/cost")
		{
			cost.GET("/summary", s.getCostSummary)
			cost.GET("/namespaces", s.getNamespaceCosts)
			cost.GET("/namespaces/:namespace", s.getNamespaceCostDetails)
			cost.GET("/nodes", s.getNodeCosts)
			cost.GET("/nodes/:node", s.getNodeCostDetails)
			cost.GET("/deployments", s.getDeploymentCosts)
			cost.GET("/deployments/:deployment", s.getDeploymentCostDetails)
			cost.GET("/trends", s.getCostTrends)
			cost.GET("/forecast", s.getCostForecast)
		}

		// Optimization endpoints
		optimize := api.Group("/optimize")
		{
			optimize.GET("/recommendations", s.getOptimizationRecommendations)
			optimize.POST("/apply/:id", s.applyOptimizationRecommendation)
			optimize.GET("/history", s.getOptimizationHistory)
			optimize.GET("/savings", s.getSavingsSummary)
			optimize.POST("/simulate", s.simulateOptimization)
		}

		// Config endpoints
		config := api.Group("/config")
		{
			config.GET("/", s.getConfiguration)
			config.PUT("/", s.updateConfiguration)
		}

		// Reports endpoints
		reports := api.Group("/reports")
		{
			reports.GET("/cost", s.generateCostReport)
			reports.GET("/optimization", s.generateOptimizationReport)
			reports.GET("/health", s.generateHealthReport)
		}
	}

	s.router = r
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware() gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		Formatter: func(param gin.LogFormatterParams) string {
			return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
				param.ClientIP,
				param.TimeStamp.Format(time.RFC1123),
				param.Method,
				param.Path,
				param.Request.Proto,
				param.StatusCode,
				param.Latency,
				param.Request.UserAgent(),
				param.ErrorMessage,
			)
		},
	})
}

// authMiddleware handles request authentication
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Implement authentication based on config type (JWT, Basic, etc.)
		switch s.config.Authentication.Type {
		case "jwt":
			token := c.GetHeader("Authorization")
			if token == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
				return
			}

			// Validate JWT token (simplified for example)
			if !validateJWT(token, s.config.Authentication.JWTKey) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
				return
			}

		case "basic":
			// Implement basic auth
			user, pass, ok := c.Request.BasicAuth()
			if !ok || !validateBasicAuth(user, pass) {
				c.Header("WWW-Authenticate", "Basic realm=KubeCostGuard")
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
				return
			}

		default:
			// Unknown auth type
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Invalid auth configuration"})
			return
		}

		c.Next()
	}
}

// corsMiddleware handles CORS
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Authorization, Content-Type")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// Start begins serving the API
func (s *Server) Start() error {
	// Configure HTTP server
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: s.router,
	}

	// Start server with TLS if enabled
	if s.config.TLSEnabled {
		return s.httpServer.ListenAndServeTLS(s.config.TLSCertPath, s.config.TLSKeyPath)
	}

	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Handler functions

// getHealthStatus returns the overall health status
func (s *Server) getHealthStatus(c *gin.Context) {
	metrics := s.health.GetMetrics()

	// Determine overall status
	status := "healthy"
	for key, value := range metrics {
		if key == "overall_status" {
			if v, ok := value.(string); ok && v != "healthy" {
				status = v
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":    status,
		"timestamp": time.Now().UTC(),
	})
}

// getHealthMetrics returns detailed health metrics
func (s *Server) getHealthMetrics(c *gin.Context) {
	metrics := s.health.GetMetrics()
	c.JSON(http.StatusOK, metrics)
}

// getHealthAlerts returns active health alerts
func (s *Server) getHealthAlerts(c *gin.Context) {
	// Implementation would depend on alert manager's interface
	c.JSON(http.StatusOK, gin.H{
		"alerts": []string{}, // Replace with actual alerts
	})
}

// getCostSummary returns cost summary information
func (s *Server) getCostSummary(c *gin.Context) {
	costData := s.cost.GetCostData()

	// Calculate total cost
	var totalCost float64
	for _, cost := range costData {
		totalCost += cost
	}

	c.JSON(http.StatusOK, gin.H{
		"total_cost": totalCost,
		"details":    costData,
		"timestamp":  time.Now().UTC(),
	})
}

// getNamespaceCosts returns costs by namespace
func (s *Server) getNamespaceCosts(c *gin.Context) {
	// Get detailed cost breakdown by namespace
	namespaceCosts, err := s.cost.GetNamespaceCosts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var response []NamespaceCostData
	for namespace, data := range namespaceCosts {
		response = append(response, NamespaceCostData{
			Name:        namespace,
			TotalCost:   data.TotalCost,
			CPUCost:     data.CPUCost,
			MemoryCost:  data.MemoryCost,
			StorageCost: data.StorageCost,
			PodCount:    data.PodCount,
		})
	}

	c.JSON(http.StatusOK, gin.H{"namespaces": response})
}

// getNamespaceCostDetails returns detailed costs for a namespace
func (s *Server) getNamespaceCostDetails(c *gin.Context) {
	namespace := c.Param("namespace")

	details, err := s.cost.GetNamespaceCostDetails(namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, details)
}

// getNodeCosts returns costs by node
func (s *Server) getNodeCosts(c *gin.Context) {
	// Get detailed cost breakdown by node
	nodeCosts, err := s.cost.GetNodeCosts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var response []NodeCostData
	for nodeName, data := range nodeCosts {
		response = append(response, NodeCostData{
			Name:              nodeName,
			TotalCost:         data.TotalCost,
			CPUCost:           data.CPUCost,
			MemoryCost:        data.MemoryCost,
			InstanceType:      data.InstanceType,
			CPUUtilization:    data.CPUUtilization,
			MemoryUtilization: data.MemoryUtilization,
		})
	}

	c.JSON(http.StatusOK, gin.H{"nodes": response})
}

// getNodeCostDetails returns detailed costs for a node
func (s *Server) getNodeCostDetails(c *gin.Context) {
	node := c.Param("node")

	details, err := s.cost.GetNodeCostDetails(node)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, details)
}

// getDeploymentCosts returns costs by deployment
func (s *Server) getDeploymentCosts(c *gin.Context) {
	deploymentCosts, err := s.cost.GetDeploymentCosts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deployments": deploymentCosts})
}

// getDeploymentCostDetails returns detailed costs for a deployment
func (s *Server) getDeploymentCostDetails(c *gin.Context) {
	deployment := c.Param("deployment")
	namespace := c.Query("namespace")

	details, err := s.cost.GetDeploymentCostDetails(deployment, namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, details)
}

// getCostTrends returns cost trends over time
func (s *Server) getCostTrends(c *gin.Context) {
	duration := c.Query("duration")
	if duration == "" {
		duration = "7d" // Default to 7 days
	}

	trends, err := s.cost.GetCostTrends(duration)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, trends)
}

// getCostForecast returns cost forecast
func (s *Server) getCostForecast(c *gin.Context) {
	duration := c.Query("duration")
	if duration == "" {
		duration = "30d" // Default to 30 days forecast
	}

	forecast, err := s.cost.GetCostForecast(duration)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, forecast)
}

// getOptimizationRecommendations returns optimization recommendations
func (s *Server) getOptimizationRecommendations(c *gin.Context) {
	summary, err := s.optimizer.GetOptimizationSummary()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, summary)
}

// applyOptimizationRecommendation applies a specific recommendation
func (s *Server) applyOptimizationRecommendation(c *gin.Context) {
	id := c.Param("id")

	result, err := s.optimizer.ApplyRecommendation(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      id,
		"status":  "applied",
		"message": "Recommendation applied successfully",
		"result":  result,
	})
}

// getOptimizationHistory returns history of applied optimizations
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

	c.JSON(http.StatusOK, gin.H{"history": history})
}

// getSavingsSummary returns summary of cost savings
func (s *Server) getSavingsSummary(c *gin.Context) {
	savings, err := s.optimizer.GetSavingsSummary()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, savings)
}

// simulateOptimization simulates optimization impact
func (s *Server) simulateOptimization(c *gin.Context) {
	var request struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	simulation, err := s.optimizer.SimulateOptimization(request.Type, request.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, simulation)
}

// getConfiguration returns the current configuration
func (s *Server) getConfiguration(c *gin.Context) {
	// Return configuration (sensitive fields redacted)
	// This is a placeholder
	c.JSON(http.StatusOK, gin.H{
		"health": gin.H{
			"scrape_interval_seconds": 60,
			"enabled_collectors":      []string{"node", "pod", "controlplane"},
		},
		"cost": gin.H{
			"update_interval_minutes": 15,
			"cloud_provider":          "aws",
		},
		"optimization": gin.H{
			"enable_auto_scaling":         true,
			"optimization_interval_hours": 24,
			"apply_recommendations":       false,
			"dry_run":                     true,
		},
	})
}

// updateConfiguration updates the configuration
func (s *Server) updateConfiguration(c *gin.Context) {
	var configUpdate struct {
		Health       map[string]interface{} `json:"health"`
		Cost         map[string]interface{} `json:"cost"`
		Optimization map[string]interface{} `json:"optimization"`
	}

	if err := c.ShouldBindJSON(&configUpdate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Apply configuration updates
	// This is a placeholder
	c.JSON(http.StatusOK, gin.H{
		"status":  "updated",
		"message": "Configuration updated successfully",
	})
}

// generateCostReport generates a cost report
func (s *Server) generateCostReport(c *gin.Context) {
	format := c.Query("format")
	if format == "" {
		format = "json"
	}

	report, err := s.cost.GenerateReport(format)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if format == "csv" {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment; filename=cost-report.csv")
		c.String(http.StatusOK, report)
	} else {
		c.JSON(http.StatusOK, report)
	}
}

// generateOptimizationReport generates an optimization report
func (s *Server) generateOptimizationReport(c *gin.Context) {
	format := c.Query("format")
	if format == "" {
		format = "json"
	}

	report, err := s.optimizer.GenerateReport(format)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if format == "pdf" {
		c.Header("Content-Type", "application/pdf")
		c.Header("Content-Disposition", "attachment; filename=optimization-report.pdf")
		c.Data(http.StatusOK, "application/pdf", []byte(report))
	} else {
		c.JSON(http.StatusOK, report)
	}
}

// generateHealthReport generates a health report
func (s *Server) generateHealthReport(c *gin.Context) {
	format := c.Query("format")
	if format == "" {
		format = "json"
	}

	report, err := s.health.GenerateReport(format)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, report)
}

// Helper functions

// validateJWT validates a JWT token
func validateJWT(token, key string) bool {
	// Implement JWT validation
	// This is a placeholder
	return token != ""
}

// validateBasicAuth validates basic auth credentials
func validateBasicAuth(username, password string) bool {
	// Implement basic auth validation
	// This is a placeholder
	return username != "" && password != ""
}
