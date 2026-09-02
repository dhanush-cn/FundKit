package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"api-gateway/middleware"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func main() {
	r := gin.Default()
	
	// Apply CORS and Rate Limiting globally
	r.Use(corsMiddleware())
	r.Use(middleware.RateLimitMiddleware())

	// Public Routes
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "UP", "service": "api-gateway"})
	})
	r.GET("/services/health", servicesHealth)

	// Mock Login Route to generate a dummy JWT for testing
	r.POST("/login", func(c *gin.Context) {
		secret := os.Getenv("JWT_SECRET")
		if secret == "" {
			secret = "dev-secret"
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-123",
			"exp": time.Now().Add(time.Hour * 24).Unix(),
		})
		tokenString, err := token.SignedString([]byte(secret))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": tokenString})
	})

	// Protected Routes
	protected := r.Group("/")
	protected.Use(middleware.JWTAuthMiddleware())
	{
		// Proxy for Order Service
		protected.Any("/orders/*path", proxyTo(orderServiceURL()))
		protected.Any("/orders", proxyTo(orderServiceURL()))
		protected.Any("/portfolio/*path", proxyTo(orderServiceURL()))
		protected.Any("/portfolio", proxyTo(orderServiceURL()))
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("API Gateway running on port %s", port)
	r.Run(":" + port)
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "http://localhost:5173")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func orderServiceURL() string {
	if value := os.Getenv("ORDER_SERVICE_URL"); value != "" {
		return value
	}
	return "http://localhost:8081"
}

func servicesHealth(c *gin.Context) {
	type serviceStatus struct {
		Service string `json:"service"`
		URL     string `json:"url"`
		Status  string `json:"status"`
	}

	services := []struct {
		name string
		url  string
	}{
		{name: "gateway", url: "http://localhost:8080/health"},
		{name: "order-service", url: orderServiceHealthURL()},
		{name: "portfolio-service", url: portfolioServiceHealthURL()},
		{name: "notification-service", url: notificationServiceHealthURL()},
	}

	client := &http.Client{Timeout: 2 * time.Second}
	results := make([]serviceStatus, 0, len(services))
	allUp := true

	for _, service := range services {
		status := serviceStatus{Service: service.name, URL: service.url, Status: "DOWN"}
		resp, err := client.Get(service.url)
		if err == nil {
			var payload map[string]interface{}
			_ = json.NewDecoder(resp.Body).Decode(&payload)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				status.Status = "UP"
			} else {
				allUp = false
			}
		} else {
			allUp = false
		}
		results = append(results, status)
	}

	if allUp {
		c.JSON(http.StatusOK, gin.H{"status": "UP", "services": results})
		return
	}
	c.JSON(http.StatusPartialContent, gin.H{"status": "DEGRADED", "services": results})
}

func orderServiceHealthURL() string {
	if value := os.Getenv("ORDER_SERVICE_HEALTH_URL"); value != "" {
		return value
	}
	return "http://localhost:8081/health"
}

func portfolioServiceHealthURL() string {
	if value := os.Getenv("PORTFOLIO_SERVICE_HEALTH_URL"); value != "" {
		return value
	}
	return "http://localhost:8082/health"
}

func notificationServiceHealthURL() string {
	if value := os.Getenv("NOTIFICATION_SERVICE_HEALTH_URL"); value != "" {
		return value
	}
	return "http://localhost:8083/health"
}

func proxyTo(target string) gin.HandlerFunc {
	return func(c *gin.Context) {
		remote, _ := url.Parse(target)
		proxy := httputil.NewSingleHostReverseProxy(remote)
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}
