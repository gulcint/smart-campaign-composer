// Package provider is the HTTP client of the SMS/email provider (mock-provider.md). It imports no
// domain package.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
)

type Channel string // "sms" | "email" (own type: provider must not import delivery)

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
)

type Message struct {
	DeliveryID string // sent as Idempotency-Key
	Channel    Channel
	To         string
	SMSBody    string // sms
	Subject    string // email
	Text       string // email plain text
	HTML       string // email html (content.RenderEmailHTML)
}

type Response struct {
	StatusCode        int
	RetryAfter        time.Duration
	HasRetryAfter     bool
	ProviderMessageID string // from body "id" on 2xx
	Replayed          bool   // response header Idempotent-Replayed: true
}

// Client sends one message. Any HTTP response (any status) is returned with err == nil.
// err != nil only for transport failures: ErrTimeout (deadline/timeout) or ErrConnection
// (refused, reset, EOF before response — includes "response lost").
type Client interface {
	Send(ctx context.Context, m Message) (Response, error)
}

var (
	ErrTimeout    = errors.New("provider: timeout")
	ErrConnection = errors.New("provider: connection error")
)

// maxResponseBody bounds how much of a response body is read.
const maxResponseBody = 64 << 10

type httpClient struct {
	baseURL string
	hc      *http.Client
	clock   clock.Clock
}

// NewHTTPClient returns a Client for the provider at baseURL. hc carries the request timeout
// (PROVIDER_TIMEOUT); c is used to resolve HTTP-date Retry-After values.
func NewHTTPClient(baseURL string, hc *http.Client, c clock.Clock) Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &httpClient{baseURL: strings.TrimRight(baseURL, "/"), hc: hc, clock: c}
}

type smsRequest struct {
	To   string `json:"to"`
	Body string `json:"body"`
}

type emailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

func (c *httpClient) Send(ctx context.Context, m Message) (Response, error) {
	var (
		path    string
		payload any
	)
	switch m.Channel {
	case ChannelSMS:
		path, payload = "/v1/sms/messages", smsRequest{To: m.To, Body: m.SMSBody}
	case ChannelEmail:
		path, payload = "/v1/email/messages", emailRequest{To: m.To, Subject: m.Subject, Text: m.Text, HTML: m.HTML}
	default:
		return Response{}, fmt.Errorf("provider: unknown channel %q", m.Channel)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, fmt.Errorf("provider: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("provider: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", m.DeliveryID)
	// net/http silently re-sends a request that carries an Idempotency-Key when a reused connection
	// closes before the response, if it can rewind the body. That hidden second request would
	// bypass the dispatcher's rate limiter and hide the connection error from the retry policy, so
	// the body is made non-rewindable: every request start is the dispatcher's own decision.
	req.GetBody = nil

	resp, err := c.hc.Do(req)
	if err != nil {
		return Response{}, transportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return Response{}, transportError(err)
	}

	out := Response{
		StatusCode: resp.StatusCode,
		Replayed:   strings.EqualFold(resp.Header.Get("Idempotent-Replayed"), "true"),
	}
	out.RetryAfter, out.HasRetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"), c.clock.Now())
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		var accepted struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &accepted) == nil {
			out.ProviderMessageID = accepted.ID
		}
	}
	return out, nil
}

// transportError maps a failed exchange to ErrTimeout or ErrConnection, keeping the cause.
func transportError(err error) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %w", ErrConnection, err)
}

// ParseRetryAfter parses delay-seconds ("2") or an HTTP-date relative to now.
// Returns (0,false) for empty or unparsable values; past dates => (0,true).
func ParseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if isDigits(v) {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, false
		}
		if n > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64), true
		}
		return time.Duration(n) * time.Second, true
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return 0, false
	}
	if d := t.Sub(now); d > 0 {
		return d, true
	}
	return 0, true
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
