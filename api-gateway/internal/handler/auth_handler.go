// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/domain"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/middleware"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/service"
)

// AuthHandler is the credential surface of the platform: the only place a
// password is ever accepted, and the only place a token is ever minted.
type AuthHandler struct {
	auth *service.AuthService
}

func NewAuthHandler(auth *service.AuthService) *AuthHandler {
	return &AuthHandler{auth: auth}
}

type registerRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required"`
	Phone    string `json:"phone" binding:"required"`
	FullName string `json:"full_name" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// userView is the outward shape of an account. It exists so that adding a
// column to domain.User can never accidentally publish it.
type userView struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	FullName string `json:"full_name"`
}

func toUserView(user *domain.User) userView {
	return userView{
		ID:       user.ID,
		Username: user.Username,
		Email:    user.Email,
		Phone:    user.Phone,
		FullName: user.FullName,
	}
}

type sessionResponse struct {
	Token     string   `json:"token"`
	UserID    string   `json:"user_id"`
	ExpiresAt string   `json:"expires_at"`
	User      userView `json:"user"`
}

// Register handles POST /auth/register.
func (h *AuthHandler) Register(c *gin.Context) {
	var request registerRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.auth.Register(c.Request.Context(), domain.Registration{
		Username: request.Username,
		Email:    request.Email,
		Phone:    request.Phone,
		FullName: request.FullName,
		Password: request.Password,
	})
	if err != nil {
		writeAuthError(c, err)
		return
	}

	// Registering logs you in: a separate round trip to /auth/login would only
	// re-verify a password the user typed two seconds ago.
	session, err := h.auth.IssueToken(user)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	c.JSON(http.StatusCreated, toSessionResponse(session))
}

// Login handles POST /auth/login.
func (h *AuthHandler) Login(c *gin.Context) {
	var request loginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	user, err := h.auth.Authenticate(c.Request.Context(), request.Username, request.Password)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	session, err := h.auth.IssueToken(user)
	if err != nil {
		writeAuthError(c, err)
		return
	}

	c.JSON(http.StatusOK, toSessionResponse(session))
}

// Me handles GET /auth/me and answers from the database rather than from the
// token, so a profile edit is visible before the token expires.
func (h *AuthHandler) Me(c *gin.Context) {
	userID := c.GetString(middleware.ContextUserID)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	user, err := h.auth.Profile(c.Request.Context(), userID)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserView(user))
}

func toSessionResponse(session service.Session) sessionResponse {
	return sessionResponse{
		Token:     session.Token,
		UserID:    session.User.ID,
		ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
		User:      toUserView(session.User),
	}
}

// writeAuthError is the single mapping from identity failures to status codes.
func writeAuthError(c *gin.Context, err error) {
	var validation domain.ValidationError
	switch {
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, gin.H{"error": validation.Error(), "field": validation.Field})
	case errors.Is(err, domain.ErrUsernameTaken), errors.Is(err, domain.ErrEmailTaken):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
	case errors.Is(err, domain.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}
