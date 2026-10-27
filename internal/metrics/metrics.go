// Package metrics exposes Prometheus metrics (docs/contracts/metrics.md).
//
// The delivery and campaign series of the API process are derived from the
// database at scrape time, so they are consistent no matter which worker
// processed a delivery.
package metrics

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// DeliveryStat is one row of: SELECT channel, status, failure_reason, count(*), sum(retry_count) ... GROUP BY.
type DeliveryStat struct {
	Channel       string
	Status        string
	FailureReason string // "" unless failed
	Count         int64
	Retries       int64
}

// DeliveryStatsSource reads the aggregates the API series are derived from.
type DeliveryStatsSource interface {
	DeliveryStats(ctx context.Context) ([]DeliveryStat, error)
	CampaignStates(ctx context.Context) (map[string]int64, error)
}

// Closed label sets: every combination is published, with 0 when there is no row.
var (
	channels       = []string{"sms", "email"}
	failureReasons = []string{"provider_rejected", "max_attempts_exceeded", "rate_limit_budget_exhausted", "content_too_long", "recipient_unavailable"}
	campaignStates = []string{"draft", "audience_ready", "content_ready", "approved", "sending", "completed"}
)

const scrapeTimeout = 2 * time.Second

// Process-wide API series (metrics.md §1) that are incremented in the request path rather than
// derived from the database. They are registered by NewAPIHandler.
var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_http_requests_total", Help: "HTTP responses by method, route template and status.",
	}, []string{"method", "route", "status"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_http_request_duration_seconds", Help: "HTTP response time by method and route template (SSE routes excluded).",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	llmRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_llm_requests_total", Help: "LLM attempts by operation, provider and outcome.",
	}, []string{"operation", "provider", "outcome"})
	llmRepairs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_llm_repairs_total", Help: "Repair attempts (attempt 2) started, by operation.",
	}, []string{"operation"})
)

// ObserveHTTPRequest counts one HTTP response. route is the route template (e.g.
// "/api/campaigns/{campaign_id}"), never the raw path. Streaming responses are counted but not
// timed.
func ObserveHTTPRequest(method, route string, status int, d time.Duration, streaming bool) {
	httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	if !streaming {
		httpDuration.WithLabelValues(method, route).Observe(d.Seconds())
	}
}

// ObserveLLMAttempt counts one LLM attempt; operation is "segment" or "content", outcome is
// "valid", "invalid" or "unavailable".
func ObserveLLMAttempt(operation, provider, outcome string) {
	llmRequests.WithLabelValues(operation, provider, outcome).Inc()
}

// ObserveLLMRepair counts one started repair attempt.
func ObserveLLMRepair(operation string) {
	llmRepairs.WithLabelValues(operation).Inc()
}

// NewAPIHandler serves /metrics for cmd/api (metrics.md §1); DB-derived series are collected at
// scrape time. If the database cannot be read, the DB-derived series are omitted from that scrape.
func NewAPIHandler(src DeliveryStatsSource) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newDBCollector(src),
		httpRequests, httpDuration, llmRequests, llmRepairs,
	)
	return contentType(promhttp.HandlerFor(reg, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError}))
}

// textFormat is the Content-Type of metrics.md. client_golang appends "; escaping=underscores"
// to the text format; every scc_ name is a legacy-valid Prometheus name, so the plain 0.0.4
// media type describes the body exactly.
const textFormat = "text/plain; version=0.0.4; charset=utf-8"

func contentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&ctWriter{ResponseWriter: w}, r)
	})
}

type ctWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *ctWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
			w.Header().Set("Content-Type", textFormat)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *ctWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

type dbCollector struct {
	src                             DeliveryStatsSource
	sent, failed, retries           *prometheus.Desc
	queued, inFlight, campaignGauge *prometheus.Desc
}

func newDBCollector(src DeliveryStatsSource) *dbCollector {
	return &dbCollector{
		src:           src,
		sent:          prometheus.NewDesc("scc_deliveries_sent_total", "Deliveries in status sent.", []string{"channel"}, nil),
		failed:        prometheus.NewDesc("scc_deliveries_failed_total", "Deliveries in status failed, by failure reason.", []string{"channel", "reason"}, nil),
		retries:       prometheus.NewDesc("scc_delivery_retries_total", "Scheduled delivery retries (sum of retry_count).", []string{"channel"}, nil),
		queued:        prometheus.NewDesc("scc_deliveries_queued", "Deliveries in status queued (including those waiting for a retry).", []string{"channel"}, nil),
		inFlight:      prometheus.NewDesc("scc_deliveries_in_flight", "Deliveries in status sending.", []string{"channel"}, nil),
		campaignGauge: prometheus.NewDesc("scc_campaigns", "Campaigns per state.", []string{"state"}, nil),
	}
}

func (c *dbCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.sent, c.failed, c.retries, c.queued, c.inFlight, c.campaignGauge} {
		ch <- d
	}
}

func (c *dbCollector) Collect(ch chan<- prometheus.Metric) {
	if c.src == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	if stats, err := c.src.DeliveryStats(ctx); err == nil {
		type key struct{ channel, reason string }
		sent := map[string]int64{}
		queued := map[string]int64{}
		inFlight := map[string]int64{}
		retries := map[string]int64{}
		failed := map[key]int64{}
		for _, s := range stats {
			retries[s.Channel] += s.Retries
			switch s.Status {
			case "sent":
				sent[s.Channel] += s.Count
			case "queued":
				queued[s.Channel] += s.Count
			case "sending":
				inFlight[s.Channel] += s.Count
			case "failed":
				failed[key{s.Channel, s.FailureReason}] += s.Count
			}
		}
		for _, chn := range channels {
			ch <- prometheus.MustNewConstMetric(c.sent, prometheus.CounterValue, float64(sent[chn]), chn)
			ch <- prometheus.MustNewConstMetric(c.retries, prometheus.CounterValue, float64(retries[chn]), chn)
			ch <- prometheus.MustNewConstMetric(c.queued, prometheus.GaugeValue, float64(queued[chn]), chn)
			ch <- prometheus.MustNewConstMetric(c.inFlight, prometheus.GaugeValue, float64(inFlight[chn]), chn)
			for _, r := range failureReasons {
				ch <- prometheus.MustNewConstMetric(c.failed, prometheus.CounterValue, float64(failed[key{chn, r}]), chn, r)
			}
		}
	}

	if states, err := c.src.CampaignStates(ctx); err == nil {
		for _, s := range campaignStates {
			ch <- prometheus.MustNewConstMetric(c.campaignGauge, prometheus.GaugeValue, float64(states[s]), s)
		}
	}
}
