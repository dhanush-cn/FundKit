// Package service holds gateway-side use cases that are more than pure
// transport glue.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
package service

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/dhanush-cn/fundkit/api-gateway/internal/config"
	"github.com/dhanush-cn/fundkit/api-gateway/internal/platform/trace"
)

// ServiceStatus is one upstream's reported condition.
type ServiceStatus struct {
	Service string `json:"service"`
	URL     string `json:"url"`
	Status  string `json:"status"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

// StackStatus is the aggregate the dashboard renders.
type StackStatus struct {
	Status   string          `json:"status"`
	Services []ServiceStatus `json:"services"`
}

// HealthAggregator fans out to every upstream probe concurrently. Sequential
// probing would make the dashboard's latency the sum of its dependencies'
// timeouts; in parallel it is the slowest one.
type HealthAggregator struct {
	targets []config.HealthTarget
	client  *http.Client
}

func NewHealthAggregator(cfg config.UpstreamConfig) *HealthAggregator {
	return &HealthAggregator{
		targets: cfg.HealthTargets,
		client:  &http.Client{Timeout: cfg.HealthTimeout},
	}
}

// Collect probes every upstream and summarises the result.
func (a *HealthAggregator) Collect(ctx context.Context) StackStatus {
	results := make([]ServiceStatus, len(a.targets))

	var wg sync.WaitGroup
	for i, target := range a.targets {
		wg.Add(1)
		go func(index int, target config.HealthTarget) {
			defer wg.Done()
			results[index] = a.probe(ctx, target)
		}(i, target)
	}
	wg.Wait()

	stack := StackStatus{Status: "UP", Services: append(
		[]ServiceStatus{{Service: "api-gateway", URL: "self", Status: "UP"}},
		results...,
	)}
	for _, result := range results {
		if result.Status != "UP" {
			stack.Status = "DEGRADED"
			break
		}
	}
	return stack
}

func (a *HealthAggregator) probe(ctx context.Context, target config.HealthTarget) ServiceStatus {
	status := ServiceStatus{Service: target.Name, URL: target.URL, Status: "DOWN"}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	if id := trace.FromContext(ctx); id != "" {
		request.Header.Set(trace.HeaderKey, id)
	}

	start := time.Now()
	response, err := a.client.Do(request)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	defer response.Body.Close()

	status.Latency = time.Since(start).Round(time.Millisecond).String()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		status.Status = "UP"
		return status
	}
	status.Error = response.Status
	return status
}
