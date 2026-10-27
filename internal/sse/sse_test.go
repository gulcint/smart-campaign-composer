package sse_test

// Unit tests of the SSE writer: content streams to the screen over SSE (REQ-CNT-04).

import (
	"bytes"
	"encoding/json"
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

// REQ-CNT-04
// The transport is SSE: NewWriter sets the event-stream content type and writes no body.
func TestNewWriter_SetsHeadersAndWritesNothing(t *testing.T) {
	t.Parallel()
	_, rec := newWriter(t)
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/event-stream; charset=utf-8", got)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("NewWriter wrote %q, want nothing", rec.Body.String())
	}
}

type deltaLike struct {
	Attempt int    `json:"attempt"`
	Channel string `json:"channel"`
	Field   string `json:"field"`
	Text    string `json:"text"`
}

// REQ-CNT-04
// Event writes one SSE event ("event: <name>" + one JSON data line) and flushes, so the content
// reaches the client piece by piece; the data decodes to the value given (Turkish letters, line
// breaks, quotes, markup).
func TestWriter_Event_OneSingleLineDataFrame(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data any
	}{
		{name: "plain delta", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: "Merhaba {{first_"}},
		{name: "newline inside the text", data: deltaLike{Attempt: 1, Channel: "email", Field: "body", Text: "Merhaba,\n\nİndirim seni bekliyor."}},
		{name: "Turkish letters", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: "ğüşİıöç ĞÜŞIÖÇ"}},
		{name: "quotes and backslashes", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: `"tırnak" \ ters`}},
		{name: "markup characters", data: deltaLike{Attempt: 1, Channel: "sms", Field: "body", Text: "<b>%30</b> & <script>alert(1)</script>"}},
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
