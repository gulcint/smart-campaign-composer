package sse_test

// Unit tests of the SSE writer (go-interfaces.md §internal/sse) against the frame rules of
// sse-events.md §0. Only the contracted surface is used: NewWriter, Event, EventWithID, Comment,
// Retry and ErrStreamingUnsupported.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/sse"
)

func newWriter(t *testing.T) (*sse.Writer, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	w, err := sse.NewWriter(rec)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	return w, rec
}

// frameLines splits one complete frame ("...\n\n") into its lines; it fails if the frame does not
// end with exactly one blank line.
func frameLines(t *testing.T, out string) []string {
	t.Helper()
	if !strings.HasSuffix(out, "\n\n") || strings.HasSuffix(out, "\n\n\n") {
		t.Fatalf("frame %q does not end with exactly one blank line", out)
	}
	return strings.Split(strings.TrimSuffix(out, "\n\n"), "\n")
}

// assertCompactJSON fails unless s is a single-line JSON object with no insignificant whitespace,
// and returns it decoded.
func assertCompactJSON(t *testing.T, s string) any {
	t.Helper()
	if strings.ContainsAny(s, "\r\n") {
		t.Fatalf("data %q contains a raw line break", s)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		t.Fatalf("data %q is not JSON: %v", s, err)
	}
	if buf.String() != s {
		t.Fatalf("data %q is not compact JSON (compact form %q)", s, buf.String())
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if _, ok := v.(map[string]any); !ok {
		t.Fatalf("data %q is not a JSON object", s)
	}
	return v
}

func decoded(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	return out
}

// REQ-CNT-04, REQ-TRK-01 (sse-events.md §0, go-interfaces §sse)
// NewWriter sets the three SSE headers and writes no body.
func TestNewWriter_SetsHeadersAndWritesNothing(t *testing.T) {
	t.Parallel()
	_, rec := newWriter(t)
	want := map[string]string{
		"Content-Type":      "text/event-stream; charset=utf-8",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("NewWriter wrote %q, want nothing", rec.Body.String())
	}
}

// noFlushWriter is an http.ResponseWriter without Flush (and without Unwrap).
type noFlushWriter struct {
	h   http.Header
	buf bytes.Buffer
}

func (w *noFlushWriter) Header() http.Header         { return w.h }
func (w *noFlushWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *noFlushWriter) WriteHeader(int)             {}

// REQ-CNT-04 (go-interfaces §sse)
// A ResponseWriter that cannot flush is refused with ErrStreamingUnsupported.
func TestNewWriter_RequiresAFlusher(t *testing.T) {
	t.Parallel()
	_, err := sse.NewWriter(&noFlushWriter{h: http.Header{}})
	if !errors.Is(err, sse.ErrStreamingUnsupported) {
		t.Fatalf("NewWriter(non-flusher) error = %v, want %v", err, sse.ErrStreamingUnsupported)
	}
}

type deltaLike struct {
	Attempt int    `json:"attempt"`
	Channel string `json:"channel"`
	Field   string `json:"field"`
	Text    string `json:"text"`
}

// REQ-CNT-04, REQ-AI-01 (sse-events.md §0)
// Event writes exactly "event: <name>\ndata: <one-line compact JSON>\n\n" and flushes: no id: or
// retry: line, never a multi-line data field (a line break inside a text is escaped inside the
// JSON), and the data decodes to the value given (Turkish letters, quotes, backslashes, markup).
func TestWriter_Event_OneSingleLineDataFrame(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data any
	}{
		{name: "plain delta", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: "Merhaba {{first_"}},
		{name: "newline inside the text", data: deltaLike{Attempt: 1, Channel: "email", Field: "body", Text: "Merhaba,\n\nİndirim seni bekliyor."}},
		{name: "carriage return and CRLF inside the text", data: deltaLike{Attempt: 2, Channel: "email", Field: "body", Text: "a\rb\r\nc"}},
		{name: "Turkish letters", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: "ğüşİıöç ĞÜŞIÖÇ"}},
		{name: "quotes and backslashes", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: `"tırnak" \ ters`}},
		{name: "markup characters", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: "<b>%30</b> & <script>alert(1)</script>"}},
		{name: "nested object", data: map[string]any{"attempt": 1, "final": true, "errors": []any{map[string]any{"path": "", "code": "internal_error", "message": "x"}}, "content_version_id": nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, rec := newWriter(t)
			if err := w.Event("delta", tc.data); err != nil {
				t.Fatalf("Event() error = %v", err)
			}
			lines := frameLines(t, rec.Body.String())
			if len(lines) != 2 {
				t.Fatalf("frame lines = %q, want exactly an event line and one data line", lines)
			}
			if lines[0] != "event: delta" {
				t.Fatalf("first line = %q, want %q", lines[0], "event: delta")
			}
			data, ok := strings.CutPrefix(lines[1], "data: ")
			if !ok {
				t.Fatalf("second line = %q, want a data: line", lines[1])
			}
			got := assertCompactJSON(t, data)
			if diff := cmp.Diff(decoded(t, tc.data), got); diff != "" {
				t.Fatalf("data mismatch (-want +got):\n%s", diff)
			}
			if !rec.Flushed {
				t.Fatal("Event did not flush")
			}
		})
	}
}

// REQ-CNT-04 (sse-events.md §0)
// A line break in a text is carried as the two characters \n inside the JSON string.
func TestWriter_Event_NewlineIsEscapedInsideTheJSON(t *testing.T) {
	t.Parallel()
	w, rec := newWriter(t)
	if err := w.Event("delta", deltaLike{Attempt: 1, Channel: "email", Field: "body", Text: "satır1\nsatır2"}); err != nil {
		t.Fatalf("Event() error = %v", err)
	}
	lines := frameLines(t, rec.Body.String())
	if len(lines) != 2 || !strings.Contains(lines[1], `satır1\nsatır2`) {
		t.Fatalf("frame lines = %q, want one data line containing the escaped sequence satır1\\nsatır2", lines)
	}
}

// REQ-TRK-01 (sse-events.md §0, §2.1)
// EventWithID puts the id line first: "id: <n>\nevent: <name>\ndata: <json>\n\n".
func TestWriter_EventWithID(t *testing.T) {
	t.Parallel()
	w, rec := newWriter(t)
	status := map[string]any{"campaign_id": "6b0e1f8a-3c2d-4e5f-8a9b-0c1d2e3f4a5b", "state": "sending"}
	if err := w.EventWithID(7, "status", status); err != nil {
		t.Fatalf("EventWithID() error = %v", err)
	}
	lines := frameLines(t, rec.Body.String())
	if len(lines) != 3 || lines[0] != "id: 7" || lines[1] != "event: status" || !strings.HasPrefix(lines[2], "data: ") {
		t.Fatalf("frame lines = %q, want [\"id: 7\" \"event: status\" \"data: {...}\"]", lines)
	}
	if diff := cmp.Diff(decoded(t, status), assertCompactJSON(t, strings.TrimPrefix(lines[2], "data: "))); diff != "" {
		t.Fatalf("data mismatch (-want +got):\n%s", diff)
	}
}

// REQ-CNT-04, REQ-TRK-01 (sse-events.md §0)
// Comment writes ": <text>\n\n"; Retry writes "retry: <ms>\n\n"; neither adds an event, id or data line.
func TestWriter_CommentAndRetry(t *testing.T) {
	t.Parallel()
	t.Run("comment", func(t *testing.T) {
		t.Parallel()
		w, rec := newWriter(t)
		if err := w.Comment("keepalive"); err != nil {
			t.Fatalf("Comment() error = %v", err)
		}
		if got := rec.Body.String(); got != ": keepalive\n\n" {
			t.Fatalf("Comment wrote %q, want %q", got, ": keepalive\n\n")
		}
	})
	t.Run("retry", func(t *testing.T) {
		t.Parallel()
		w, rec := newWriter(t)
		if err := w.Retry(3000); err != nil {
			t.Fatalf("Retry() error = %v", err)
		}
		if got := rec.Body.String(); got != "retry: 3000\n\n" {
			t.Fatalf("Retry wrote %q, want %q", got, "retry: 3000\n\n")
		}
	})
}

// REQ-CNT-04, REQ-TRK-01 (sse-events.md §0, §2.3)
// Frames are written back to back in call order; id: and retry: lines appear only where asked for.
func TestWriter_SequenceOfFrames(t *testing.T) {
	t.Parallel()
	w, rec := newWriter(t)
	steps := []func() error{
		func() error { return w.Retry(3000) },
		func() error { return w.EventWithID(1, "snapshot", map[string]any{"state": "sending"}) },
		func() error { return w.Comment("keepalive") },
		func() error { return w.Event("delta", deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: "x"}) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d error = %v", i, err)
		}
	}
	blocks := strings.Split(strings.TrimSuffix(rec.Body.String(), "\n\n"), "\n\n")
	var heads []string
	for _, b := range blocks {
		var keys []string
		for _, l := range strings.Split(b, "\n") {
			switch {
			case strings.HasPrefix(l, ":"):
				keys = append(keys, "comment")
			case strings.HasPrefix(l, "data: "):
				keys = append(keys, "data")
			default:
				keys = append(keys, l)
			}
		}
		heads = append(heads, strings.Join(keys, "|"))
	}
	want := []string{"retry: 3000", "id: 1|event: snapshot|data", "comment", "event: delta|data"}
	if diff := cmp.Diff(want, heads); diff != "" {
		t.Fatalf("frame sequence mismatch (-want +got):\n%s\nbody: %q", diff, rec.Body.String())
	}
}
