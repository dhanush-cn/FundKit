// Engineered by Dhanush C N (github.com/dhanush-cn)
package handler

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
)

// ProxyHandler forwards authenticated traffic to an upstream service.
//
// The reverse proxy (and its connection pool) is built once at startup. The
// previous implementation constructed a new proxy per request, which threw away
// keep-alive connections and forced a fresh TCP handshake on every call.
type ProxyHandler struct {
	proxy  *httputil.ReverseProxy
	logger *slog.Logger
}

func NewProxyHandler(target string, timeout time.Duration, logger *slog.Logger) (*ProxyHandler, error) {
	remote, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(remote)
	proxy.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: timeout,
	}

	// An upstream failure must surface as a gateway error the caller can act
	// on, not as httputil's default bare 502 with an empty body.
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.ErrorContext(r.Context(), "upstream proxy failure",
			slog.String("upstream", remote.String()),
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()),
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream service unavailable"}`))
	}

	return &ProxyHandler{proxy: proxy, logger: logger}, nil
}

// Handle serves one proxied request.
func (h *ProxyHandler) Handle(c *gin.Context) {
	h.proxy.ServeHTTP(c.Writer, c.Request)
}
