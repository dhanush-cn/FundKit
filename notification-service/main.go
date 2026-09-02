package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	log.Println("Notification Service Started...")
	log.Println("Consuming events from Kafka topics...")

	go startHTTPServer()
	
	// Start Kafka consumer in background
	go startKafkaConsumer()

	// Keep the service running
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	log.Println("Waiting for order events... (Press Ctrl+C to stop)")
	<-sigChan
	log.Println("Notification Service shutting down...")
}

func startHTTPServer() {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "UP",
			"service": "notification-service",
			"ts":      time.Now().UTC().Format(time.RFC3339),
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8083"
	}

	if err := r.Run(":" + port); err != nil {
		log.Printf("notification HTTP server stopped: %v", err)
	}
}
