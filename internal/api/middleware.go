package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5" // Ensure this package is installed
	"golang.org/x/time/rate"
)

// rateLimiter holds rate limiter for each IP address
type rateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	limit    rate.Limit
	burst    int
}

// newRateLimiter creates a new rate limiter
func newRateLimiter(reqPerSec float64, burst int) *rateLimiter {
	rl := &rateLimiter{
		limiters: make(map[string]*rate.Limiter),
		limit:    rate.Limit(reqPerSec),
		burst:    burst,
	}

	// Cleanup old limiters every 10 minutes
	go rl.cleanupRoutine()

	return rl
}

// getLimiter returns rate limiter for an IP
func (rl *rateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if limiter, exists := rl.limiters[ip]; exists {
		return limiter
	}

	limiter := rate.NewLimiter(rl.limit, rl.burst)
	rl.limiters[ip] = limiter
	return limiter
}

// cleanupRoutine removes old limiters
func (rl *rateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(10 * time.Minute)
	for range ticker.C {
		rl.mu.Lock()
		for ip, limiter := range rl.limiters {
			if limiter.Tokens() == float64(rl.burst) {
				delete(rl.limiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// AuthMiddleware handles authentication
func AuthMiddleware(jwtKey string, authEnabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !authEnabled {
			c.Next()
			return
		}

		// Check for JWT token in Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Authorization header required",
			})
			c.Abort()
			return
		}

		// Validate Bearer token format
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid authorization header format",
			})
			c.Abort()
			return
		}

		// Validate JWT token
		token, err := validateJWT(parts[1], jwtKey)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": fmt.Sprintf("Invalid token: %v", err),
			})
			c.Abort()
			return
		}

		// Add user info to context
		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			c.Set("user", claims["user"])
			c.Set("roles", claims["roles"])
		}

		c.Next()
	}
}

// validateJWT validates JWT token
func ValidateJWT(tokenString, key string) (*jwt.Token, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(key), nil
	})

	return token, err
}

// BasicAuthMiddleware handles basic authentication
func BasicAuthMiddleware(users map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.Header("WWW-Authenticate", `Basic realm="Restricted"`)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Authorization required",
			})
			c.Abort()
			return
		}

		const prefix = "Basic "
		if !strings.HasPrefix(auth, prefix) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid authorization format",
			})
			c.Abort()
			return
		}

		decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid authorization encoding",
			})
			c.Abort()
			return
		}

		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid credentials format",
			})
			c.Abort()
			return
		}

		username, password := parts[0], parts[1]
		expectedPassword, ok := users[username]
		if !ok || subtle.ConstantTimeCompare([]byte(password), []byte(expectedPassword)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid credentials",
			})
			c.Abort()
			return
		}

		c.Set("user", username)
		c.Next()
	}
}

// RateLimitMiddleware implements rate limiting per IP
func RateLimitMiddleware(reqPerSec float64, burst int) gin.HandlerFunc {
	limiter := newRateLimiter(reqPerSec, burst)

	return func(c *gin.Context) {
		ip := getClientIP(c)
		ipLimiter := limiter.getLimiter(ip)

		if !ipLimiter.Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "Rate limit exceeded",
				"retry_after": int(ipLimiter.Reserve().Delay().Seconds()),
			})
			c.Abort()
			return
		}

		// Add rate limit headers
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", burst))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", int(ipLimiter.Burst())))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Second).Unix()))

		c.Next()
	}
}

// RecoveryMiddleware recovers from panics and returns 500
func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				stack := debug.Stack()
				log.Printf("Panic recovered: %v\n%s", err, stack)

				c.JSON(http.StatusInternalServerError, gin.H{
					"error":      "Internal server error",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}

// RequestIDMiddleware adds request ID to context
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := generateRequestID()
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware() gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		Formatter: func(param gin.LogFormatterParams) string {
			return fmt.Sprintf("[%s] %s %s %s %d %s %s\n",
				param.TimeStamp.Format("2006-01-02 15:04:05"),
				param.Method,
				param.Path,
				param.ClientIP,
				param.StatusCode,
				param.Latency,
				param.ErrorMessage,
			)
		},
		SkipPaths: []string{"/healthz", "/metrics"},
	})
}

// CORSMiddleware handles Cross-Origin Resource Sharing
func CORSMiddleware(allowedOrigins []string, allowCredentials bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// Check if origin is allowed
		allowed := false
		for _, allowedOrigin := range allowedOrigins {
			if allowedOrigin == "*" || origin == allowedOrigin {
				allowed = true
				break
			}
		}

		if allowed {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		// Set CORS headers
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		c.Header("Access-Control-Max-Age", "3600")

		if allowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		// Handle preflight requests
		if c.Request.Method == "OPTIONS" {
			c.JSON(http.StatusNoContent, nil)
			c.Abort()
			return
		}

		c.Next()
	}
}

// SecurityHeadersMiddleware adds security headers
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Prevent clickjacking
		c.Header("X-Frame-Options", "SAMEORIGIN")

		// Prevent XSS attacks
		c.Header("X-XSS-Protection", "1; mode=block")

		// Prevent MIME sniffing
		c.Header("X-Content-Type-Options", "nosniff")

		// Enforce HTTPS
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Content Security Policy
		c.Header("Content-Security-Policy", "default-src 'self'")

		// Referrer Policy
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		c.Next()
	}
}

// MetricsMiddleware collects metrics for requests
func MetricsMiddleware(collector MetricsCollector) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start)

		// Collect metrics
		collector.RecordRequest(
			c.Request.Method,
			c.Request.URL.Path,
			c.Writer.Status(),
			duration,
		)
	}
}

// MetricsCollector interface for collecting metrics
type MetricsCollector interface {
	RecordRequest(method, path string, status int, duration time.Duration)
}

// RequestContextMiddleware adds useful context values
func RequestContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), "request_time", time.Now())
		ctx = context.WithValue(ctx, "correlation_id", c.GetString("request_id"))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// CompressionMiddleware handles response compression
func CompressionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Don't compress already compressed content or streams
		contentType := c.Writer.Header().Get("Content-Type")
		if strings.Contains(contentType, "stream") ||
			strings.Contains(contentType, "gzip") ||
			strings.Contains(contentType, "video") {
			return
		}

		// Check if client accepts compression
		acceptEncoding := c.GetHeader("Accept-Encoding")
		if !strings.Contains(acceptEncoding, "gzip") {
			return
		}

		// For small responses, compression overhead might not be worth it
		if c.Writer.Size() < 1024 {
			return
		}

		c.Header("Content-Encoding", "gzip")
	}
}

// ErrorHandlingMiddleware standardizes error responses
func ErrorHandlingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) > 0 {
			err := c.Errors.Last()

			var status int
			var message string

			// Determine status code from error type
			switch err.Type {
			case gin.ErrorTypeBind:
				status = http.StatusBadRequest
				message = "Invalid request format"
			case gin.ErrorTypePublic:
				status = http.StatusInternalServerError
				message = err.Error()
			default:
				status = http.StatusInternalServerError
				message = "Internal server error"
			}

			c.JSON(status, gin.H{
				"error":      message,
				"request_id": c.GetString("request_id"),
			})
		}
	}
}

// TimeoutMiddleware implements request timeout
func TimeoutMiddleware(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)

		done := make(chan struct{})
		go func() {
			c.Next()
			done <- struct{}{}
		}()

		select {
		case <-done:
			return
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				c.JSON(http.StatusGatewayTimeout, gin.H{
					"error":      "Request timeout",
					"request_id": c.GetString("request_id"),
				})
				c.Abort()
			}
		}
	}
}

// Helper functions

func getClientIP(c *gin.Context) string {
	// Check for X-Forwarded-For header (load balancer/proxy)
	if ip := c.GetHeader("X-Forwarded-For"); ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0])
	}

	// Check for X-Real-IP header
	if ip := c.GetHeader("X-Real-IP"); ip != "" {
		return ip
	}

	// Fall back to RemoteAddr
	ip := c.ClientIP()
	return ip
}

func generateRequestID() string {
	// Generate a unique request ID (simplified)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Nanosecond())
}
