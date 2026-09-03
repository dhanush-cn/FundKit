// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import "github.com/gin-gonic/gin"

// Headers the api-gateway stamps onto every authenticated request after it has
// verified the bearer token. Internal services trust them because the gateway
// strips whatever the client sent before setting its own values, and because
// nothing but the gateway is routable from outside the cluster.
const (
	HeaderUserID    = "x-fundkit-user-id"
	HeaderUsername  = "x-fundkit-username"
	HeaderUserEmail = "x-fundkit-user-email"
	HeaderUserPhone = "x-fundkit-user-phone"
	HeaderUserName  = "x-fundkit-user-name"
)

// callerIdentity is the verified customer behind a request.
type callerIdentity struct {
	UserID   string
	Username string
	Email    string
	Phone    string
	FullName string
}

// identityFromHeaders reads whatever the gateway forwarded. An empty struct is
// a legitimate outcome: the service is also driven directly in local
// development and by integration tests, where no gateway is in front of it.
func identityFromHeaders(c *gin.Context) callerIdentity {
	return callerIdentity{
		UserID:   c.GetHeader(HeaderUserID),
		Username: c.GetHeader(HeaderUsername),
		Email:    c.GetHeader(HeaderUserEmail),
		Phone:    c.GetHeader(HeaderUserPhone),
		FullName: c.GetHeader(HeaderUserName),
	}
}

// displayName prefers the registered full name and falls back to the username,
// so a notification never opens with a raw UUID.
func (i callerIdentity) displayName() string {
	if i.FullName != "" {
		return i.FullName
	}
	return i.Username
}
