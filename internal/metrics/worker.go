package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// providerCodes is the closed code label set of scc_provider_requests_total.
var providerCodes = []string{"2xx", "408", "429", "4xx", "5xx", "timeout", "connection_error"}

// WorkerMetrics holds the worker process series (metrics.md §2) and implements delivery.Recorder.
type WorkerMetrics struct {
	providerRequests *prometheus.CounterVec
	providerDuration *prometheus.HistogramVec
	leader           *prometheus.GaugeVec
	limiterWait      *prometheus.HistogramVec
}

// NewWorkerHandler serves /metrics for cmd/worker (metrics.md §2). Each call has its own registry.
func NewWorkerHandler() (http.Handler, *WorkerMetrics) {
	m := &WorkerMetrics{
		providerRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_provider_requests_total", Help: "Finished provider requests by channel and response code class.",
		}, []string{"channel", "code"}),
		providerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "scc_provider_request_duration_seconds", Help: "Provider request duration by channel.",
			Buckets: prometheus.DefBuckets,
		}, []string{"channel"}),
		leader: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "scc_dispatcher_leader", Help: "1 if this process holds the channel's dispatcher lock, else 0.",
		}, []string{"channel"}),
		limiterWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "scc_rate_limiter_wait_seconds", Help: "Time spent waiting for a rate limiter permit, by channel.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"channel"}),
	}
	for _, ch := range channels {
		m.leader.WithLabelValues(ch).Set(0)
		for _, code := range providerCodes {
			m.providerRequests.WithLabelValues(ch, code)
		}
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.providerRequests, m.providerDuration, m.leader, m.limiterWait,
	)
	return contentType(promhttp.HandlerFor(reg, promhttp.HandlerOpts{})), m
}

// ObserveProviderResponse counts one finished provider request (scc_provider_requests_total) and
// its duration.
func (m *WorkerMetrics) ObserveProviderResponse(channel string, code string, d time.Duration) {
	m.providerRequests.WithLabelValues(channel, code).Inc()
	m.providerDuration.WithLabelValues(channel).Observe(d.Seconds())
}

// ObserveLimiterWait records one RateLimiter.Wait duration (scc_rate_limiter_wait_seconds).
func (m *WorkerMetrics) ObserveLimiterWait(channel string, d time.Duration) {
	m.limiterWait.WithLabelValues(channel).Observe(d.Seconds())
}

// SetLeader publishes scc_dispatcher_leader{channel}.
func (m *WorkerMetrics) SetLeader(channel string, leader bool) {
	v := 0.0
	if leader {
		v = 1
	}
	m.leader.WithLabelValues(channel).Set(v)
}
