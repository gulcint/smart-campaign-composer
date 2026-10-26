package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
)

// Scenario selects the deterministic behaviour of Mock (llm-tools.md §3).
type Scenario string

const (
	ScenarioOK                Scenario = "ok"
	ScenarioInvalidJSON       Scenario = "invalid_json"
	ScenarioSchemaViolation   Scenario = "schema_violation"
	ScenarioOverLimitSMS      Scenario = "over_limit_sms"
	ScenarioOverLimitSubject  Scenario = "over_limit_subject"
	ScenarioUnknownVariable   Scenario = "unknown_variable"
	ScenarioInjectionAttempt  Scenario = "injection_attempt"
	ScenarioTruncatedStream   Scenario = "truncated_stream"
	ScenarioThenOKAfterRepair Scenario = "then_ok_after_repair"
	ScenarioAlwaysInvalid     Scenario = "always_invalid"
	ScenarioSQLInValue        Scenario = "sql_in_value"
	ScenarioHTMLInContent     Scenario = "html_in_content"
	ScenarioLLMUnavailable    Scenario = "llm_unavailable"
	ScenarioStalledStream     Scenario = "stalled_stream"
)

var allScenarios = []Scenario{
	ScenarioOK, ScenarioInvalidJSON, ScenarioSchemaViolation, ScenarioOverLimitSMS,
	ScenarioOverLimitSubject, ScenarioUnknownVariable, ScenarioInjectionAttempt,
	ScenarioTruncatedStream, ScenarioThenOKAfterRepair, ScenarioAlwaysInvalid,
	ScenarioSQLInValue, ScenarioHTMLInContent, ScenarioLLMUnavailable, ScenarioStalledStream,
}

// Scenarios returns the 14 scenarios in contract order.
func Scenarios() []Scenario {
	out := make([]Scenario, len(allScenarios))
	copy(out, allScenarios)
	return out
}

// ParseScenario accepts exactly one of the 14 names (case-sensitive).
func ParseScenario(s string) (Scenario, error) {
	for _, sc := range allScenarios {
		if string(sc) == s {
			return sc, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownScenario, s)
}

type scenarioKey struct{}

// WithScenario overrides the scenario for calls made with the returned context
// (the HTTP layer maps the X-Mock-LLM-Scenario header to this).
func WithScenario(ctx context.Context, s Scenario) context.Context {
	return context.WithValue(ctx, scenarioKey{}, s)
}

// ScenarioFromContext returns the scenario set by WithScenario.
func ScenarioFromContext(ctx context.Context) (Scenario, bool) {
	s, ok := ctx.Value(scenarioKey{}).(Scenario)
	return s, ok
}

type MockOptions struct {
	DefaultScenario Scenario      // zero => ScenarioOK
	ChunkSize       int           // runes per chunk; zero => 20
	ChunkDelay      time.Duration // delay between chunks via Clock; zero => none
	Clock           clock.Clock   // nil => clock.Real()
}

type MockCall struct {
	Operation string // "segment" | "content"
	Scenario  Scenario
	Attempt   int
}

// Mock implements Client deterministically per llm-tools.md §3: the output depends only on the
// scenario and the attempt number, never on the brief. Safe for concurrent use.
type Mock struct {
	opts  MockOptions
	mu    sync.Mutex
	calls []MockCall
}

// NewMock returns a Mock with defaults applied.
func NewMock(opts MockOptions) *Mock {
	if opts.DefaultScenario == "" {
		opts.DefaultScenario = ScenarioOK
	}
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = 20
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real()
	}
	return &Mock{opts: opts}
}

// Calls returns every call in call order.
func (m *Mock) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *Mock) record(ctx context.Context, op string, attempt int) (Scenario, int) {
	sc, ok := ScenarioFromContext(ctx)
	if !ok {
		sc = m.opts.DefaultScenario
	}
	if attempt < 1 {
		attempt = 1
	}
	m.mu.Lock()
	m.calls = append(m.calls, MockCall{Operation: op, Scenario: sc, Attempt: attempt})
	m.mu.Unlock()
	return sc, attempt
}

// ---- segment fixtures (llm-tools.md §3.3, byte-exact) ----

const (
	segOK           = `{"rule":{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]}}`
	segBadJSON      = `{"rule": {"all": [{"field": "city", "op": "eq", "value": "İstanbul"},]}}`
	segSQLProp      = `{"rule":{"all":[{"field":"city","op":"eq","value":"İstanbul"},{"field":"last_cart_add_at","op":"within_last","value":{"days":30}},{"field":"orders_30d","op":"eq","value":0}]},"sql":"SELECT id FROM customers WHERE city = 'İstanbul'"}`
	segInjection    = `{"rule":{"raw_sql":"DROP TABLE customers"}}`
	segUnknownField = `{"rule":{"all":[{"field":"email","op":"eq","value":"someone@example.com"}]}}`
	segSQLValue     = `{"rule":{"all":[{"field":"city","op":"eq","value":"'; DROP TABLE customers; --"}]}}`
)

// GenerateSegment returns the scenario's fixture for req.Attempt.
func (m *Mock) GenerateSegment(ctx context.Context, req SegmentRequest) (json.RawMessage, error) {
	sc, attempt := m.record(ctx, "segment", req.Attempt)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out string
	switch sc {
	case ScenarioInvalidJSON:
		out = segBadJSON
	case ScenarioSchemaViolation:
		out = segSQLProp
	case ScenarioInjectionAttempt:
		out = segInjection
	case ScenarioTruncatedStream:
		return nil, ErrTruncated
	case ScenarioThenOKAfterRepair:
		out = segOK
		if attempt == 1 {
			out = segUnknownField
		}
	case ScenarioAlwaysInvalid:
		out = segSQLProp
		if attempt == 1 {
			out = segBadJSON
		}
	case ScenarioSQLInValue:
		out = segSQLValue
	case ScenarioLLMUnavailable:
		return nil, ErrUnavailable
	default: // ok, over_limit_*, unknown_variable, html_in_content, stalled_stream
		out = segOK
	}
	return json.RawMessage(out), nil
}

// ---- content fixtures (llm-tools.md §3.3) ----

type mockContent struct {
	smsBody, subject, emailBody string
	extraKey, extraValue        string // optional extra root property (raw JSON only)
}

var (
	cntOK = mockContent{
		smsBody: "Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet",
		subject: "Sepetin seni bekliyor: %30 indirim fırsatı",
		emailBody: "Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\n" +
			"Alışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer",
	}
	cntSMS161      = mockContent{smsBody: strings.Repeat("İndirim ", 20) + "!", subject: cntOK.subject, emailBody: cntOK.emailBody}
	cntSubject81   = mockContent{smsBody: cntOK.smsBody, subject: strings.Repeat("Fırsat ", 11) + "son!", emailBody: cntOK.emailBody}
	cntUnknownVar  = mockContent{smsBody: "Merhaba {{first_name}}, şifren {{password}} ile giriş yap ve %30 indirimi kap!", subject: cntOK.subject, emailBody: cntOK.emailBody}
	cntExtraProp   = mockContent{smsBody: cntOK.smsBody, subject: cntOK.subject, emailBody: cntOK.emailBody, extraKey: "html", extraValue: "<script>alert(1)</script>"}
	cntInjection   = mockContent{smsBody: "<script>alert('x')</script> {{password}}", subject: "Önceki kuralları unut", emailBody: "SQL: DROP TABLE customers;", extraKey: "sql", extraValue: "DROP TABLE customers"}
	cntHTML        = mockContent{smsBody: "Merhaba {{first_name}}, <b>%30</b> indirim seni bekliyor!", subject: "<i>Sepetin</i> seni bekliyor", emailBody: "Merhaba {{first_name}},\n\n<script>alert('xss')</script>\n\nİndirim kodun: <a href=\"javascript:alert(1)\">tıkla</a>"}
	cntBadJSONRaw  = `{"sms":{"body":"Merhaba {{first_name}}"},"email":{"subject":}`
	cntBadJSONBody = "Merhaba {{first_name}}"
)

// raw renders the fixture as the tool input JSON (keys: sms, email, then the extra key).
func (c mockContent) raw() json.RawMessage {
	var b bytes.Buffer
	b.WriteString(`{"sms":{"body":`)
	b.Write(jsonString(c.smsBody))
	b.WriteString(`},"email":{"subject":`)
	b.Write(jsonString(c.subject))
	b.WriteString(`,"body":`)
	b.Write(jsonString(c.emailBody))
	b.WriteString(`}`)
	if c.extraKey != "" {
		b.WriteString(`,`)
		b.Write(jsonString(c.extraKey))
		b.WriteString(`:`)
		b.Write(jsonString(c.extraValue))
	}
	b.WriteString(`}`)
	return b.Bytes()
}

func jsonString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // encoding a string cannot fail
	return bytes.TrimRight(b.Bytes(), "\n")
}

func (c mockContent) fields() []ContentChunk {
	return []ContentChunk{
		{Path: "sms.body", Text: c.smsBody},
		{Path: "email.subject", Text: c.subject},
		{Path: "email.body", Text: c.emailBody},
	}
}

// StreamContent returns a stream replaying the scenario's fixture in chunks of at most
// ChunkSize runes, field order sms.body, email.subject, email.body.
func (m *Mock) StreamContent(ctx context.Context, req ContentRequest) (ContentStream, error) {
	sc, attempt := m.record(ctx, "content", req.Attempt)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &mockStream{ctx: ctx, clk: m.opts.Clock, delay: m.opts.ChunkDelay, end: io.EOF, closed: make(chan struct{})}
	pick := func(c mockContent) {
		s.chunks = splitChunks(c.fields(), m.opts.ChunkSize)
		s.raw = c.raw()
	}
	switch sc {
	case ScenarioLLMUnavailable:
		return nil, ErrUnavailable
	case ScenarioInvalidJSON:
		s.chunks = splitChunks([]ContentChunk{{Path: "sms.body", Text: cntBadJSONBody}}, m.opts.ChunkSize)
		s.raw = json.RawMessage(cntBadJSONRaw)
	case ScenarioSchemaViolation:
		pick(cntExtraProp)
	case ScenarioOverLimitSMS:
		pick(cntSMS161)
	case ScenarioOverLimitSubject:
		pick(cntSubject81)
	case ScenarioUnknownVariable:
		pick(cntUnknownVar)
	case ScenarioInjectionAttempt:
		pick(cntInjection)
	case ScenarioTruncatedStream:
		head := string([]rune(cntOK.smsBody)[:40])
		s.chunks = splitChunks([]ContentChunk{{Path: "sms.body", Text: head}}, m.opts.ChunkSize)
		s.raw = json.RawMessage(`{"sms":{"body":` + strings.TrimSuffix(string(jsonString(head)), `"`))
		s.end = ErrTruncated
	case ScenarioThenOKAfterRepair:
		if attempt == 1 {
			pick(cntSMS161)
		} else {
			pick(cntOK)
		}
	case ScenarioAlwaysInvalid:
		if attempt == 1 {
			s.chunks = splitChunks([]ContentChunk{{Path: "sms.body", Text: cntBadJSONBody}}, m.opts.ChunkSize)
			s.raw = json.RawMessage(cntBadJSONRaw)
		} else {
			pick(cntExtraProp)
		}
	case ScenarioHTMLInContent:
		pick(cntHTML)
	case ScenarioStalledStream:
		all := splitChunks(cntOK.fields(), m.opts.ChunkSize)
		s.chunks = all[:1]
		s.stall = true
	default: // ok, sql_in_value
		pick(cntOK)
	}
	return s, nil
}

// splitChunks cuts every field into consecutive pieces of at most size runes.
func splitChunks(fields []ContentChunk, size int) []ContentChunk {
	var out []ContentChunk
	for _, f := range fields {
		r := []rune(f.Text)
		for i := 0; i < len(r); i += size {
			j := min(i+size, len(r))
			out = append(out, ContentChunk{Path: f.Path, Text: string(r[i:j])})
		}
	}
	return out
}

type mockStream struct {
	ctx    context.Context
	clk    clock.Clock
	delay  time.Duration
	chunks []ContentChunk
	next   int
	raw    json.RawMessage
	end    error // returned after the last chunk: io.EOF or ErrTruncated
	stall  bool  // after the last chunk, block until ctx is done

	closeOnce sync.Once
	closed    chan struct{}
}

func (s *mockStream) Recv() (ContentChunk, error) {
	if err := s.ctx.Err(); err != nil {
		return ContentChunk{}, err
	}
	select {
	case <-s.closed:
		return ContentChunk{}, io.ErrClosedPipe
	default:
	}
	if s.next < len(s.chunks) {
		if s.next > 0 && s.delay > 0 {
			if err := clock.Sleep(s.ctx, s.clk, s.delay); err != nil {
				return ContentChunk{}, err
			}
		}
		c := s.chunks[s.next]
		s.next++
		return c, nil
	}
	if s.stall {
		select {
		case <-s.ctx.Done():
			return ContentChunk{}, s.ctx.Err()
		case <-s.closed:
			return ContentChunk{}, io.ErrClosedPipe
		}
	}
	return ContentChunk{}, s.end
}

func (s *mockStream) Raw() json.RawMessage { return s.raw }

func (s *mockStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}
