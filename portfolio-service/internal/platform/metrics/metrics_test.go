// Engineered by Dhanush C N (github.com/dhanush-cn)
package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestSplitFullMethod covers the parsing that every gRPC label depends on.
// The "unknown" fallbacks are the important rows: passing a malformed method
// name through unchanged would let a bad client inflate label cardinality.
func TestSplitFullMethod(t *testing.T) {
	cases := []struct {
		name        string
		fullMethod  string
		wantService string
		wantMethod  string
	}{
		{"canonical", "/fundkit.PortfolioService/GetUserPnL", "fundkit.PortfolioService", "GetUserPnL"},
		{"health check", "/grpc.health.v1.Health/Check", "grpc.health.v1.Health", "Check"},
		{"no leading slash", "fundkit.PortfolioService/GetUserPnL", "fundkit.PortfolioService", "GetUserPnL"},
		{"no separator", "/GetUserPnL", "unknown", "unknown"},
		{"empty", "", "unknown", "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, method := splitFullMethod(tc.fullMethod)
			if service != tc.wantService || method != tc.wantMethod {
				t.Fatalf("splitFullMethod(%q) = (%q, %q), want (%q, %q)",
					tc.fullMethod, service, method, tc.wantService, tc.wantMethod)
			}
		})
	}
}

// TestInterceptorRecordsStatusCode is the reason the code label exists.
//
// NotFound and DeadlineExceeded are indistinguishable at the transport layer
// and mean completely different things to whoever is on call: one is a client
// asking for a portfolio that does not exist, the other is this service failing
// to answer in time. An unwrapped Go error must map to Unknown, which is
// exactly what the client sees on the wire.
func TestInterceptorRecordsStatusCode(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"success", nil, "OK"},
		{"typed status", status.Error(codes.NotFound, "no holdings for user"), "NotFound"},
		{"deadline", status.Error(codes.DeadlineExceeded, "nav source too slow"), "DeadlineExceeded"},
		{"untyped go error", errors.New("nil map write"), "Unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := New()
			interceptor := registry.GRPC.UnaryInterceptor()

			info := &grpc.UnaryServerInfo{FullMethod: "/fundkit.PortfolioService/GetUserPnL"}
			_, err := interceptor(context.Background(), nil, info,
				func(context.Context, any) (any, error) { return nil, tc.err })

			if !errors.Is(err, tc.err) {
				t.Fatalf("interceptor swallowed or altered the handler error: %v", err)
			}

			expected := `
# HELP grpc_server_requests_total Total unary RPCs handled, by service, method and gRPC status code.
# TYPE grpc_server_requests_total counter
grpc_server_requests_total{grpc_code="` + tc.wantCode + `",grpc_method="GetUserPnL",grpc_service="fundkit.PortfolioService"} 1
`
			if err := testutil.GatherAndCompare(
				registry.Gatherer(), strings.NewReader(expected), "grpc_server_requests_total",
			); err != nil {
				t.Fatalf("status code label: %v", err)
			}
		})
	}
}

// TestInterceptorTimesFailedCalls: a failing RPC is still an RPC that consumed
// time, and a call that fails slowly is a far worse symptom than one that fails
// fast. Timing only the successes would make a degrading dependency invisible.
func TestInterceptorTimesFailedCalls(t *testing.T) {
	registry := New()
	interceptor := registry.GRPC.UnaryInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/fundkit.PortfolioService/GetUserPnL"}

	_, _ = interceptor(context.Background(), nil, info,
		func(context.Context, any) (any, error) { return struct{}{}, nil })
	_, _ = interceptor(context.Background(), nil, info,
		func(context.Context, any) (any, error) { return nil, status.Error(codes.Internal, "boom") })

	if observations := histogramObservations(t, registry.GRPC.duration); observations != 2 {
		t.Fatalf("grpc_server_request_duration_seconds observations = %d, want 2", observations)
	}
}

// TestInterceptorReleasesInFlightGauge guards the deferred decrement: a handler
// that panics must not leave the saturation gauge permanently elevated.
func TestInterceptorReleasesInFlightGauge(t *testing.T) {
	registry := New()
	interceptor := registry.GRPC.UnaryInterceptor()
	info := &grpc.UnaryServerInfo{FullMethod: "/fundkit.PortfolioService/GetUserPnL"}

	_, _ = interceptor(context.Background(), nil, info,
		func(context.Context, any) (any, error) { return struct{}{}, nil })

	func() {
		defer func() { _ = recover() }()
		_, _ = interceptor(context.Background(), nil, info,
			func(context.Context, any) (any, error) { panic("handler exploded") })
	}()

	if inFlight := testutil.ToFloat64(registry.GRPC.inFlight); inFlight != 0 {
		t.Fatalf("grpc_server_requests_in_flight = %v after both calls finished, want 0", inFlight)
	}
}

func TestMetricsServerExposesPrometheusExposition(t *testing.T) {
	registry := New()
	interceptor := registry.GRPC.UnaryInterceptor()
	_, _ = interceptor(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: "/fundkit.PortfolioService/GetUserPnL"},
		func(context.Context, any) (any, error) { return struct{}{}, nil })

	recorder := httptest.NewRecorder()
	NewServer(":0", registry).Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", recorder.Code)
	}
	for _, want := range []string{"grpc_server_requests_total", "go_goroutines"} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("/metrics body is missing %q", want)
		}
	}
}

// --------------------------------------------------------------------- helpers

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
