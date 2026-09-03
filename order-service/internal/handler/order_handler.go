// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/order-service/internal/domain"
	"github.com/dhanush-cn/fundkit/order-service/internal/service"
)

// createOrderRequest is the transport-level contract. Keeping it separate from
// domain.Order stops JSON tags and validation rules from leaking into the
// business layer.
type createOrderRequest struct {
	// UserID is not required in the body: when the request arrives through the
	// gateway the verified subject wins over anything the client claims, which
	// is what stops one customer from placing an order in another's name.
	UserID         string           `json:"user_id"`
	FundID         string           `json:"fund_id" binding:"required"`
	Amount         float64          `json:"amount" binding:"required,gt=0"`
	Type           domain.OrderType `json:"type" binding:"required,oneof=SIP LUMPSUM"`
	IdempotencyKey string           `json:"idempotency_key" binding:"required"`
}

type updateOrderStatusRequest struct {
	Status domain.OrderStatus `json:"status" binding:"required,oneof=PENDING PROCESSING EXECUTED FAILED"`
}

type OrderHandler struct {
	orders *service.OrderService
}

func NewOrderHandler(orders *service.OrderService) *OrderHandler {
	return &OrderHandler{orders: orders}
}

// Create handles POST /orders.
func (h *OrderHandler) Create(c *gin.Context) {
	var request createOrderRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	caller := identityFromHeaders(c)
	userID := caller.UserID
	if userID == "" {
		userID = request.UserID
	}
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	order, err := h.orders.Place(c.Request.Context(), domain.NewOrder{
		UserID:         userID,
		UserName:       caller.displayName(),
		UserEmail:      caller.Email,
		UserPhone:      caller.Phone,
		FundID:         request.FundID,
		Amount:         request.Amount,
		Type:           request.Type,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		writeDomainError(c, err)
		return
	}

	c.JSON(http.StatusCreated, order)
}

// List handles GET /orders.
func (h *OrderHandler) List(c *gin.Context) {
	limit := 100
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	orders, err := h.orders.List(c.Request.Context(), limit)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, orders)
}

// Get handles GET /orders/:id.
func (h *OrderHandler) Get(c *gin.Context) {
	order, err := h.orders.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, order)
}

// UpdateStatus handles PATCH /orders/:id.
func (h *OrderHandler) UpdateStatus(c *gin.Context) {
	var request updateOrderStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	order, err := h.orders.UpdateStatus(c.Request.Context(), c.Param("id"), request.Status)
	if err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, order)
}

// Delete handles DELETE /orders/:id.
func (h *OrderHandler) Delete(c *gin.Context) {
	if err := h.orders.Delete(c.Request.Context(), c.Param("id")); err != nil {
		writeDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "order deleted successfully"})
}

// writeDomainError is the single place that maps domain failures onto HTTP
// status codes, so every endpoint answers consistently.
func writeDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrDuplicateOrder):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrOrderNotCancelable):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}
