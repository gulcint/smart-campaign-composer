// Package health serves the liveness and readiness endpoints (REQ-TRK-03).
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger is satisfied by *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

const readyTimeout = time.Second

// Healthz always answers 200 {"status":"ok"}; it does not look at the database.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// Readyz pings the database with a 1s timeout: 200 {"status":"ready","checks":{"database":"ok"}}
// or 503 {"status":"not_ready","checks":{"database":"unavailable"}}. The body never carries
// the database error.
func Readyz(db Pinger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		if db == nil || db.Ping(ctx) != nil {
			writeJSON(w, http.StatusServiceUnavailable, readiness{Status: "not_ready", Checks: checks{Database: "unavailable"}})
			return
		}
		writeJSON(w, http.StatusOK, readiness{Status: "ready", Checks: checks{Database: "ok"}})
	})
}

type readiness struct {
	Status string `json:"status"`
	Checks checks `json:"checks"`
}

type checks struct {
	Database string `json:"database"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the client may have gone; nothing left to do
}
