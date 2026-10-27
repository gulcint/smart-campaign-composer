// Package sse writes Server-Sent Events (sse-events.md §0): one compact JSON object per data line,
// every event flushed immediately.
package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
)

var ErrStreamingUnsupported = errors.New("sse: response writer cannot flush")

// Writer writes SSE blocks to a response. Safe for concurrent use (events and heartbeat comments
// from different goroutines never interleave).
type Writer struct {
	mu sync.Mutex
	w  http.ResponseWriter
	f  http.Flusher
}

// NewWriter sets the SSE headers (sse-events.md §0) and writes nothing else.
func NewWriter(w http.ResponseWriter) (*Writer, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, ErrStreamingUnsupported
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	return &Writer{w: w, f: f}, nil
}

// Event writes "event: name\ndata: <json>\n\n" and flushes.
func (w *Writer) Event(name string, data any) error {
	return w.event("", name, data)
}

// EventWithID writes "id: n\nevent: name\ndata: <json>\n\n" and flushes.
func (w *Writer) EventWithID(id int64, name string, data any) error {
	return w.event("id: "+strconv.FormatInt(id, 10)+"\n", name, data)
}

func (w *Writer) event(prefix, name string, data any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("sse: encode %s: %w", name, err)
	}
	// Compact JSON never contains a raw newline (string newlines are escaped), so the payload is
	// always exactly one data line; Encode's trailing newline is dropped.
	payload := bytes.TrimRight(buf.Bytes(), "\n")
	return w.write(prefix + "event: " + name + "\ndata: " + string(payload) + "\n\n")
}

// Comment writes ": text\n\n" (heartbeat) and flushes.
func (w *Writer) Comment(text string) error {
	return w.write(": " + text + "\n\n")
}

// Retry writes "retry: ms\n\n" and flushes.
func (w *Writer) Retry(ms int) error {
	return w.write("retry: " + strconv.Itoa(ms) + "\n\n")
}

// Flush sends whatever is buffered (e.g. the headers of a stream that has no event yet).
func (w *Writer) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.f.Flush()
}

func (w *Writer) write(s string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.w.Write([]byte(s)); err != nil {
		return fmt.Errorf("sse: write: %w", err)
	}
	w.f.Flush()
	return nil
}
