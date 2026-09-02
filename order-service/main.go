package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"order-service/cache"
	"order-service/database"
	"order-service/messaging"
	"order-service/models"

	"github.com/gin-gonic/gin"
)

func main() {
	// Initialize Database, Redis, and Kafka
	database.InitDB()
	cache.InitRedis()
	messaging.InitKafka()
	InitPortfolioClient()

	r := gin.Default()

	// Routes
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "UP", "service": "order-service"})
	})

	r.POST("/orders", placeOrder)
	r.GET("/orders", listOrders)
	r.GET("/orders/:id", getOrder)
	r.PATCH("/orders/:id", updateOrder)
	r.DELETE("/orders/:id", deleteOrder)
	r.GET("/portfolio/:userId/pnl", getUserPnL)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Order Service starting on port %s...", port)
	r.Run(":" + port)
}

// POST /orders
func placeOrder(c *gin.Context) {
	var request CreateOrderRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !cache.ClaimIdempotency(request.IdempotencyKey, idempotencyTTL) {
		c.JSON(http.StatusConflict, gin.H{"error": "Duplicate order request"})
		return
	}

	order := models.Order{
		UserID:         request.UserID,
		FundID:         request.FundID,
		Amount:         request.Amount,
		Type:           request.Type,
		Status:         models.StatusPending,
		IdempotencyKey: request.IdempotencyKey,
	}

	if err := database.DB.Create(&order).Error; err != nil {
		cache.ReleaseIdempotency(request.IdempotencyKey)
		if strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
			c.JSON(http.StatusConflict, gin.H{"error": "Duplicate order request"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save order"})
		return
	}

	cache.SetIdempotency(order.IdempotencyKey, idempotencyTTL)

	publishOrderSnapshot(order.ID)
	go processOrderLifecycle(order.ID)

	c.JSON(http.StatusCreated, order)
}

// GET /orders
func listOrders(c *gin.Context) {
	var orders []models.Order
	database.DB.Find(&orders)
	c.JSON(http.StatusOK, orders)
}

// GET /orders/:id
func getOrder(c *gin.Context) {
	id := c.Param("id")
	var order models.Order
	if err := database.DB.First(&order, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}
	c.JSON(http.StatusOK, order)
}

// PATCH /orders/:id
func updateOrder(c *gin.Context) {
	id := c.Param("id")
	var order models.Order
	if err := database.DB.First(&order, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}

	var request UpdateOrderStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := applyManualOrderStatus(&order, request.Status); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, order)
}

// DELETE /orders/:id
func deleteOrder(c *gin.Context) {
	id := c.Param("id")
	var order models.Order
	if err := database.DB.First(&order, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Order not found"})
		return
	}

	if order.Status != models.StatusPending {
		c.JSON(http.StatusConflict, gin.H{"error": "Only pending orders can be deleted"})
		return
	}

	if err := database.DB.Delete(&models.Order{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete order"})
		return
	}
	cache.ReleaseIdempotency(order.IdempotencyKey)
	c.JSON(http.StatusOK, gin.H{"message": "Order deleted successfully"})
}
