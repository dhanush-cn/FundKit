package main

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

var ErrPortfolioUnavailable = errors.New("portfolio service unavailable")

func getUserPnL(c *gin.Context) {
	userID := c.Param("userId")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userId is required"})
		return
	}

	response, err := FetchUserPnL(c.Request.Context(), userID)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, ErrPortfolioUnavailable) {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}
