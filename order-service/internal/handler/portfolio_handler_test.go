// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newPnLContext builds a gin.Context the way the real router would populate
// it: :userId as a route param, and the gateway's identity headers (or their
// absence) on the request. The recorder is returned alongside it so a test
// can inspect the response the handler wrote.
func newPnLContext(pathUserID string, headerUserID string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	request := httptest.NewRequest(http.MethodGet, "/portfolio/"+pathUserID+"/pnl", nil)
	if headerUserID != "" {
		request.Header.Set(HeaderUserID, headerUserID)
	}
	c.Request = request
	c.Params = gin.Params{{Key: "userId", Value: pathUserID}}
	return c, recorder
}

// This is the regression test for the fix: before it, GetUserPnL trusted
// whatever :userId the client put in the URL, so any authenticated user could
// read any other user's portfolio just by editing the address bar or the
// dashboard's "User ID" field. The gateway-verified header must win.
func TestResolvePnLUserIDPrefersVerifiedIdentityOverPathParam(t *testing.T) {
	t.Parallel()

	c, _ := newPnLContext("victim-user", "attacker-user")

	if got := resolvePnLUserID(c); got != "attacker-user" {
		t.Fatalf("resolvePnLUserID = %q, want the verified caller %q, not the path segment", got, "attacker-user")
	}
}

func TestResolvePnLUserIDFallsBackToPathParamWithoutAGateway(t *testing.T) {
	t.Parallel()

	// No HeaderUserID set: this is the local-development / integration-test
	// shape identity.go documents, where nothing sits in front of the
	// service to stamp a verified identity.
	c, _ := newPnLContext("user-1", "")

	if got := resolvePnLUserID(c); got != "user-1" {
		t.Fatalf("resolvePnLUserID = %q, want the path fallback %q", got, "user-1")
	}
}

func TestResolvePnLUserIDEmptyWhenNeitherIsSet(t *testing.T) {
	t.Parallel()

	c, _ := newPnLContext("", "")

	if got := resolvePnLUserID(c); got != "" {
		t.Fatalf("resolvePnLUserID = %q, want empty string", got)
	}
}

// GetUserPnL must reject a request with no resolvable user id before it ever
// reaches the portfolio client -- calling through with an empty id would
// depend on downstream behavior for a failure mode that should instead be a
// clean, explicit 400 raised at this edge.
func TestGetUserPnLRejectsMissingUserID(t *testing.T) {
	t.Parallel()

	// h.client is left nil deliberately: the assertion is that this code path
	// returns before ever dereferencing it.
	handler := NewPortfolioHandler(nil)

	c, recorder := newPnLContext("", "")
	handler.GetUserPnL(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}
