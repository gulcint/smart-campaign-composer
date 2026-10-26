// Package composer runs LLM generation with validation and the single repair attempt.
//
// P2 only provides the Event type the HTTP layer streams; the generation loop
// arrives with the audience and content slices (P3, P4).
package composer

// Event is one content-stream SSE event (sse-events.md §1). Name() is the SSE event name.
type Event interface{ Name() string }
