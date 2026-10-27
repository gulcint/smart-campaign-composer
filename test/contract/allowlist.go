// Package contract holds the API ↔ doc drift-guard tests (docs/contracts/README.md §11, gate
// `api-routes`) and the per-phase allowlist they share with the E2E operationId coverage check.
package contract

// NotImplemented is the single per-phase exception list of docs/contracts/go-interfaces.md §httpapi
// ("faz başına liste, operationId → faz"): operations of openapi.yaml that have no route yet in the
// current phase. Rules enforced by the tests:
//
//   - every key must be an operationId of openapi.yaml;
//   - an operation listed here must NOT have a route (once it has one, delete its line);
//   - every operation NOT listed here must have a route (test/contract) and must be called at least
//     once by the E2E suite under OPENAPI_VALIDATE=strict (test/e2e TestMain);
//   - at a phase gate (slice -> main) the list may not contain that phase's operations.
//
// Phase P3 (audience slice) implements, in openapi.yaml order: getHealthz, getReadyz, getMetrics,
// getConfig, createCampaign, listCampaigns, getCampaign, generateSegment, countSegment.
var NotImplemented = map[string]string{
	// P4 — content + approval slice (REQ-CNT, REQ-APR, REQ-UI-01).
	"generateContent":     "P4",
	"listContentVersions": "P4",
	"editContent":         "P4",
	"getContentVersion":   "P4",
	"approveContent":      "P4",
	"previewContent":      "P4",
	// P5 — send + tracking slice (REQ-SND, REQ-TRK-01).
	"sendCampaign":         "P5",
	"getCampaignStatus":    "P5",
	"streamCampaignEvents": "P5",
	"listDeliveries":       "P5",
}
