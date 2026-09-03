// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// TestMiddlewareLabelsByRouteTemplate is the cardinality guarantee, pinned.
//
// Three requests to three different order ids must collapse into ONE series
// labelled with the route template. If somebody ever swaps c.FullPath() for
// c.Request.URL.Path this test fails immediately — which matters, because in
// production that mistake is silent until the Prometheus host runs out of
// memory weeks later.
func TestMiddlewareLabelsByRouteTemplate(t *testing.T) {
	registry := New()

	router := gin.New()
	router.Use(registry.HTTP.Middleware())
	router.GET("/orders/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, id := range []string{"9f3c1e5a", "b27d4400", "c81a0e6f"} {
		perform(router, http.MethodGet, "/orders/"+id)
	}

	expected := `
# HELP http_requests_total Total HTTP requests handled, by method, route template and response status code.
# TYPE http_requests_total counter
http_requests_total{method="GET",path="/orders/:id",status_code="200"} 3
`
	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(expected), "http_requests_total",
	); err != nil {
		t.Fatalf("route template label: %v", err)
	}

	if observations := histogramObservations(t, registry.HTTP.duration); observations != 3 {
		t.Fatalf("duration observations = %d, want 3", observations)
	}
}

// TestMiddlewareFoldsUnmatchedRoutes covers the other half of the cardinality
// story: a request that matches no route has no template, and every one of them
// must land in a single bucket. Otherwise a scanner probing random URLs mints a
// series per URL it tries.
func TestMiddlewareFoldsUnmatchedRoutes(t *testing.T) {
	registry := New()

	router := gin.New()
	router.Use(registry.HTTP.Middleware())

	perform(router, http.MethodGet, "/wp-admin")
	perform(router, http.MethodGet, "/.env")

	expected := `
# HELP http_requests_total Total HTTP requests handled, by method, route template and response status code.
# TYPE http_requests_total counter
http_requests_total{method="GET",path="unmatched",status_code="404"} 2
`
	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(expected), "http_requests_total",
	); err != nil {
		t.Fatalf("unmatched routes: %v", err)
	}
}

// TestMiddlewareCountsRecoveredPanicAsServerError pins the middleware ORDERING,
// which is the subtle one.
//
// A panic unwinds through every middleware registered below the recovery
// handler before recover() runs. If the metrics middleware were registered
// after Recovery it would observe the status code as it stood before the 500
// was written — and panics would be counted as successes, which is the worst
// possible failure mode for an error-rate dashboard. Registered before it, as
// NewRouter does, c.Next() returns with the 500 already set.
func TestMiddlewareCountsRecoveredPanicAsServerError(t *testing.T) {
	registry := New()

	router := gin.New()
	router.Use(
		registry.HTTP.Middleware(),
		// Stands in for middleware.Recovery, which wraps this same gin
		// primitive; the writer is discarded to keep test output readable.
		gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}),
	)
	router.GET("/orders", func(c *gin.Context) { panic("upstream exploded") })

	if status := perform(router, http.MethodGet, "/orders").Code; status != http.StatusInternalServerError {
		t.Fatalf("response status = %d, want 500", status)
	}

	expected := `
# HELP http_requests_total Total HTTP requests handled, by method, route template and response status code.
# TYPE http_requests_total counter
http_requests_total{method="GET",path="/orders",status_code="500"} 1
`
	if err := testutil.GatherAndCompare(
		registry.Gatherer(), strings.NewReader(expected), "http_requests_total",
	); err != nil {
		t.Fatalf("recovered panic: %v", err)
	}
}

// TestMiddlewareReleasesInFlightGauge guards against the classic leak: a gauge
// incremented on the way in and decremented on a path that a panic skips. The
// counter climbs forever and the saturation panel becomes a lie.
func TestMiddlewareReleasesInFlightGauge(t *testing.T) {
	registry := New()

	router := gin.New()
	router.Use(
		registry.HTTP.Middleware(),
		gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
			c.AbortWithStatus(http.StatusInternalServerError)
		}),
	)
	router.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/boom", func(c *gin.Context) { panic("boom") })

	perform(router, http.MethodGet, "/ok")
	perform(router, http.MethodGet, "/boom")

	if inFlight := testutil.ToFloat64(registry.HTTP.inFlight); inFlight != 0 {
		t.Fatalf("http_requests_in_flight = %v after all requests completed, want 0", inFlight)
	}
}

// TestRegistryIsIsolated proves the registry is per-process state rather than a
// package-level default: two registries built in the same test binary must not
// see each other's observations, and building the second must not panic on a
// duplicate registration.
func TestRegistryIsIsolated(t *testing.T) {
	first, second := New(), New()

	router := gin.New()
	router.Use(first.HTTP.Middleware())
	router.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	perform(router, http.MethodGet, "/ping")

	if count := testutil.CollectAndCount(second.HTTP.requests, "http_requests_total"); count != 0 {
		t.Fatalf("second registry saw %d series from the first; registries are not isolated", count)
	}
}

func TestMetricsServerExposesPrometheusExposition(t *testing.T) {
	registry := New()

	router := gin.New()
	router.Use(registry.HTTP.Middleware())
	router.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	perform(router, http.MethodGet, "/ping")

	recorder := httptest.NewRecorder()
	NewServer(":0", registry).Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
	for _, want := range []string{
		`http_requests_total{method="GET",path="/ping",status_code="200"} 1`,
		"go_goroutines",
	} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("/metrics body is missing %q", want)
		}
	}
}

// --------------------------------------------------------------------- helpers

func perform(router http.Handler, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

// histogramObservations totals the sample count across every series a histogram
// collector holds. testutil has no helper for this because a histogram's _sum
// is timing-dependent and therefore cannot be compared against fixed text.
func histogramObservations(t *testing.T, collector prometheus.Collector) uint64 {
	t.Helper()

	samples := make(chan prometheus.Metric)
	go func() {
		collector.Collect(samples)
		close(samples)
	}()

	var total uint64
	for sample := range samples {
		var snapshot dto.Metric
		if err := sample.Write(&snapshot); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		total += snapshot.GetHistogram().GetSampleCount()
	}
	return total
}
