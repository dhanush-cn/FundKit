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

// GetUserPnL handles GET /portfolio/:userId/pnl.
func (h *PortfolioHandler) GetUserPnL(c *gin.Context) {
	userID := c.Param("userId")
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

	c.JSON(http.StatusOK, response)
}
