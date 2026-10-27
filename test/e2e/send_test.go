package e2e_test

// P5 send (REQ-SND-01) over HTTP against the compose.test.yml stack. Only the generation-lock bullet
// of REQ-SND-01 is covered here: "Aynı kampanyada içerik üretimi sürerken
// POST /api/campaigns/{campaign_id}/send 409 generation_in_progress döner ve hiçbir teslimat kaydı
// oluşturmaz. ... Üretim başlatma ile gönderim aynı anda gelirse ikisinden yalnızca biri kabul
// edilir."
//
// A running generation is held open with the stalled_stream mock scenario, exactly as
// TestE2E_ContentGenerationLock does (first delta, then the stream waits until the client leaves).
// "No delivery record" is observed through the campaign: it stays approved and
// sending_started_at stays null (state machine: sending is entered only when the delivery records
// are created). The send request carries no body: requirements.md names no send request body.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func sendURL(api, id string) string { return api + "/api/campaigns/" + id + "/send" }

// approvedCampaign returns an approved campaign of the example brief and its approved version id.
func approvedCampaign(t *testing.T, api string, m manifestDoc) (string, string) {
	t.Helper()
	id := audienceReady(t, api, m)
	v := generate(t, api, id, "ok").validated
	if v == nil {
		t.Fatal("setup: no validated version")
	}
	if c := mustApprove(t, api, id, v.ContentVersionID); c.State != "approved" {
		t.Fatalf("setup: state after approve = %s, want approved", c.State)
	}
	return id, v.ContentVersionID
}

// holdGeneration opens a content generation that stays running (stalled_stream: one delta, then
// it waits) and returns the function that ends it by closing the connection.
func holdGeneration(t *testing.T, api, id string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	resp := postContent(t, ctx, api, id, nil, scenario("stalled_stream"))
	release := func() {
		cancel()
		_ = resp.Body.Close()
	}
	t.Cleanup(release)
	assertSSEResponse(t, resp)
	readFirstDelta(t, resp)
	return release
}

func readFirstDelta(t *testing.T, resp *http.Response) {
	t.Helper()
	first, ok := nextEvent(t, newSSEScanner(resp))
	if !ok || first.Name != "delta" {
		t.Fatalf("first event of the running generation = %+v, want a delta", first)
	}
	validateEvent(t, first)
}

// waitGenerationReleased polls until the campaign's generation lock is free: an over-limit edit
// gets 409 generation_in_progress while the lock is held and 422 (nothing stored, approval kept,
// REQ-APR-03) once it is free.
func waitGenerationReleased(t *testing.T, api, id, base string) {
	t.Helper()
	tooLong := mkContent(strings.Repeat("a", 161), "Konu", "Gövde").json(t)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("the generation lock was not released within 15s after the client disconnected")
		case <-ticker.C:
			status, hdr, body := postEdit(t, api, id, base, tooLong)
			switch status {
			case http.StatusConflict:
				assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
			case http.StatusUnprocessableEntity:
				return
			default:
				t.Fatalf("edit after disconnect status = %d; body: %s", status, body)
			}
		}
	}
}

// assertNotSent checks the campaign is still approved for vid and never entered sending, i.e. no
// delivery records were created for it.
func assertNotSent(t *testing.T, api, id, vid, when string) {
	t.Helper()
	status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("GET campaign %s status = %d; body: %s", when, status, body)
	}
	raw := decodeObject(t, body)
	if raw["state"] != "approved" || raw["sending_started_at"] != nil {
		t.Fatalf("campaign %s state/sending_started_at = %v/%v, want approved/null (no delivery records)", when, raw["state"], raw["sending_started_at"])
	}
	var c campaignDoc
	mustUnmarshal(t, string(body), &c)
	if c.Approval == nil || c.Approval.ContentVersionID != vid {
		t.Fatalf("campaign %s approval = %+v, want bound to %s", when, c.Approval, vid)
	}
}

// REQ-SND-01
// While a content generation runs on an approved campaign, POST /api/campaigns/{campaign_id}/send is
// refused with 409 generation_in_progress, every time it is tried, and nothing is sent: the
// campaign stays approved and never enters sending, during the generation and after it ended.
func TestE2E_SendRefusedWhileGenerating(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)
	id, vid := approvedCampaign(t, api, m)

	release := holdGeneration(t, api, id)
	for i := range 2 {
		status, hdr, body := doJSON(t, http.MethodPost, sendURL(api, id), nil, nil)
		if status != http.StatusConflict {
			t.Fatalf("send #%d during the generation status = %d, want 409; body: %s", i+1, status, body)
		}
		assertError(t, status, hdr, body, http.StatusConflict, "generation_in_progress", false)
	}
	assertNotSent(t, api, id, vid, "during the generation")

	release()
	waitGenerationReleased(t, api, id, vid)
	assertNotSent(t, api, id, vid, "after the generation ended")
}

type raceReply struct {
	status int
	hdr    http.Header
	body   []byte
	resp   *http.Response // open SSE response of an accepted generation
	err    error
}

// raceDo sends one request without touching t (it runs off the test goroutine). An accepted
// generation (200) keeps its body open in resp; every other response is read and closed.
func raceDo(ctx context.Context, method, url string, headers map[string]string, keepOpenOn200 bool) raceReply {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return raceReply{err: err}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return raceReply{err: err}
	}
	r := raceReply{status: resp.StatusCode, hdr: resp.Header}
	if keepOpenOn200 && resp.StatusCode == http.StatusOK {
		r.resp = resp
		return r
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		r.err = err
	}
	r.body = buf.Bytes()
	return r
}

// REQ-SND-01
// A content generation start and a send that arrive at the same time on an approved campaign are
// never both accepted, and exactly one of them is. Either the generation wins (200 stream) and the
// send gets 409 generation_in_progress with nothing sent, or the send wins (2xx, campaign leaves
// approved for sending/completed) and the generation is refused with a 409 error body. Several
// campaigns race in parallel so both orders get a chance to occur; the invariant is checked on each.
func TestE2E_SendAndGenerationStartRace(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	m := readManifest(t)

	for _, name := range []string{"race 1", "race 2", "race 3", "race 4", "race 5", "race 6"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			id, vid := approvedCampaign(t, api, m)

			genCtx, cancelGen := context.WithCancel(t.Context())
			defer cancelGen()
			genHeaders := scenario("stalled_stream")
			genHeaders["Accept"] = "text/event-stream"

			var gen, send raceReply
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				gen = raceDo(genCtx, http.MethodPost, api+"/api/campaigns/"+id+"/content", genHeaders, true)
			}()
			go func() {
				defer wg.Done()
				<-start
				send = raceDo(t.Context(), http.MethodPost, sendURL(api, id), nil, false)
			}()
			close(start)
			wg.Wait()
			if gen.resp != nil {
				t.Cleanup(func() { cancelGen(); _ = gen.resp.Body.Close() })
			}
			if gen.err != nil || send.err != nil {
				t.Fatalf("request errors: generation %v, send %v", gen.err, send.err)
			}

			genAccepted := gen.status == http.StatusOK
			sendAccepted := send.status >= 200 && send.status < 300
			switch {
			case genAccepted && sendAccepted:
				t.Fatalf("both were accepted: generation %d, send %d; body: %s", gen.status, send.status, send.body)
			case !genAccepted && !sendAccepted:
				t.Fatalf("neither was accepted: generation %d (%s), send %d (%s)", gen.status, gen.body, send.status, send.body)
			case genAccepted:
				assertError(t, send.status, send.hdr, send.body, http.StatusConflict, "generation_in_progress", false)
				assertSSEResponse(t, gen.resp)
				readFirstDelta(t, gen.resp)
				assertNotSent(t, api, id, vid, "while the winning generation runs")
				cancelGen()
				_ = gen.resp.Body.Close()
				waitGenerationReleased(t, api, id, vid)
				assertNotSent(t, api, id, vid, "after the winning generation ended")
			default:
				if gen.status != http.StatusConflict {
					t.Fatalf("generation that lost to the send status = %d, want 409; body: %s", gen.status, gen.body)
				}
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(gen.body, &env); err != nil || env.Error.Code == "" {
					t.Fatalf("refused generation body is not an error envelope (%v): %s", err, gen.body)
				}
				status, _, body := doJSON(t, http.MethodGet, api+"/api/campaigns/"+id, nil, nil)
				if status != http.StatusOK {
					t.Fatalf("GET campaign status = %d; body: %s", status, body)
				}
				raw := decodeObject(t, body)
				if (raw["state"] != "sending" && raw["state"] != "completed") || raw["sending_started_at"] == nil {
					t.Fatalf("after the winning send state/sending_started_at = %v/%v, want sending|completed/set", raw["state"], raw["sending_started_at"])
				}
				if n := len(listVersions(t, api, id)); n != 1 {
					t.Fatalf("versions after the refused generation = %d, want only the approved one", n)
				}
			}
		})
	}
}
