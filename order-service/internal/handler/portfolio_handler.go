// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/order-service/internal/portfolio"
)

// PortfolioHandler exposes the read-through view of portfolio-service that the
// dashboard consumes over plain HTTP.
type PortfolioHandler struct {
	client *portfolio.Client
}

func NewPortfolioHandler(client *portfolio.Client) *PortfolioHandler {
	return &PortfolioHandler{client: client}
}

// resolvePnLUserID decides whose P&L this request may see. The gateway-verified
// identity wins whenever it is present, exactly like the order-creation path in
// identity.go: a client cannot use the :userId segment to read another
// customer's positions, because the gateway strips any identity header the
// client sent and stamps its own before this service ever sees the request.
//
// The path parameter is used only as a fallback for the case identity.go
// documents -- local development and integration tests that drive this
// handler directly, with no gateway in front of it to stamp an identity.
func resolvePnLUserID(c *gin.Context) string {
	if caller := identityFromHeaders(c); caller.UserID != "" {
		return caller.UserID
	}
	return c.Param("userId")
}

// GetUserPnL handles GET /portfolio/:userId/pnl.
func (h *PortfolioHandler) GetUserPnL(c *gin.Context) {
	userID := resolvePnLUserID(c)
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId is required"})
		return
	}

	response, err := h.client.FetchUserPnL(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, portfolio.ErrUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": portfolio.ErrUnavailable.Error()})
			return
		}
		_ = c.Error(err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "portfolio lookup failed"})
		return
	}

	// A user with no open positions is a legitimate, common state -- not an
	// error. portfolio-service already returns a zero-valued response for an
	// unknown or empty holdings list (service.Valuate never errors on an empty
	// slice), so this handler does not need a special case: the JSON below
	// serializes to total_value: 0, total_unrealized_gain: 0, holdings: []
	// however response.Holdings comes back, and the frontend renders that as a
	// clean ₹0.00 rather than crashing or hanging on a missing field.
	c.JSON(http.StatusOK, response)
}
