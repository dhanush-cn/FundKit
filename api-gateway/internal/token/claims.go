// Package token defines the JWT contract shared by the auth service that mints
// tokens and the middleware that verifies them.
//
// It is its own package so that neither side has to import the other: the
// issuer and the verifier only agree on this struct.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package token

import "github.com/golang-jwt/jwt/v5"

// Claims carries the identity the rest of the platform needs.
//
// Embedding the contact details is a deliberate trade. It means order-service
// and notification-service can address a customer without a synchronous lookup
// back to the gateway, at the cost of a token that goes stale if the user
// changes their email. Given a 24h TTL and the fact that the token is only used
// to *address* a notification (never to authorise one), that is the cheaper
// side of the trade.
type Claims struct {
	jwt.RegisteredClaims

	Username string `json:"username"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	FullName string `json:"name"`
}
