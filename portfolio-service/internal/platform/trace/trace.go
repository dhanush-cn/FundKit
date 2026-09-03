// Package trace carries a single correlation identifier across every hop of a
// FundKit request: HTTP header -> Go context -> gRPC metadata -> Kafka header.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// HeaderKey is the wire name used for the correlation id on every transport.
// gRPC metadata keys must be lowercase, so the same constant works everywhere.
const HeaderKey = "x-request-id"

type contextKey struct{}

var requestIDKey contextKey

// NewID returns a 128-bit random, hex-encoded correlation id. crypto/rand keeps
// the helper dependency-free across all four services.
func NewID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf)
}

// WithRequestID returns a context carrying the supplied correlation id.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// FromContext extracts the correlation id, returning "" when absent.
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// EnsureContext adopts an inbound correlation id when the caller supplied one
// and mints a fresh id otherwise. Every entry point funnels through this so a
// trace is never silently dropped at a service boundary.
func EnsureContext(ctx context.Context, inbound string) (context.Context, string) {
	id := strings.TrimSpace(inbound)
	if id == "" {
		id = NewID()
	}
	return WithRequestID(ctx, id), id
}
