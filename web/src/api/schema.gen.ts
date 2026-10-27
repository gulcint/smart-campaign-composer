// GENERATED FILE — do not edit by hand. (Excluded from ESLint and Prettier.)
// Source: docs/contracts/openapi.yaml (+ docs/contracts/schemas/*.schema.json via $ref)
// Generator: openapi-typescript (version pinned in web/package.json), web/scripts/api-types.mjs
// Regenerate with `make api-types`; CI runs `make api-types-check` and fails when this is stale.

export interface paths {
    "/healthz": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Liveness (REQ-TRK-03) */
        get: operations["getHealthz"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/readyz": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Readiness — database reachable (REQ-TRK-03) */
        get: operations["getReadyz"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/metrics": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Prometheus metrics (REQ-TRK-02, names in metrics.md) */
        get: operations["getMetrics"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/config": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Effective runtime configuration visible to the UI (REQ-AI-03) */
        get: operations["getConfig"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/docs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Interactive API documentation (Swagger UI)
         * @description HTML page that loads Swagger UI from /docs/{asset} and this contract from /openapi.yaml. It has no
         *     inline script (CSP `script-src 'self'`); the UI is started by /docs/swagger-initializer.js.
         *     Security headers on the response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
         *     `Referrer-Policy: no-referrer` and a `Content-Security-Policy` that allows only same-origin
         *     scripts and connections.
         */
        get: operations["getApiDocs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/docs/{asset}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description One of the vendored Swagger UI files (swagger-ui-dist, Apache-2.0) or the page initializer. */
                asset: "swagger-ui-bundle.js" | "swagger-ui-bundle.js.LICENSE.txt" | "swagger-ui.css" | "swagger-initializer.js" | "favicon-32x32.png" | "LICENSE" | "NOTICE";
            };
            cookie?: never;
        };
        /**
         * A file used by the documentation page
         * @description Content type by file: `.js` => `text/javascript`, `.css` => `text/css`, `.png` => `image/png`,
         *     `LICENSE`, `NOTICE` and `*.LICENSE.txt` => `text/plain`. Any other name => 404 `not_found`.
         */
        get: operations["getApiDocsAsset"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/openapi.yaml": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * This OpenAPI contract
         * @description Byte-identical to docs/contracts/openapi.yaml (the embedded copy, `make check-contract-copies`).
         *     Its relative `$ref`s resolve to /schemas/{schema}.
         */
        get: operations["getOpenApiSpec"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/schemas/{schema}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A JSON Schema file referenced by this contract. */
                schema: "segment-rule.schema.json" | "content.schema.json";
            };
            cookie?: never;
        };
        /**
         * A JSON Schema file referenced by this contract
         * @description Byte-identical to docs/contracts/schemas/{schema}. Any other name => 404 `not_found`.
         */
        get: operations["getContractSchema"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List campaigns, newest first */
        get: operations["listCampaigns"];
        put?: never;
        /**
         * Create a campaign from a brief (REQ-AUD-05)
         * @description Stores the brief (NFC-normalized, trimmed) and creates the campaign in state `draft`.
         *     Does not call the LLM. Validation: brief empty/whitespace => field code `required`;
         *     > 2000 characters (runes, after trim) => `too_long` (2000 accepted); missing `brief` key or
         *     non-string => `schema_violation`.
         */
        post: operations["createCampaign"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        /** One campaign with its segment, latest validated content version and approval */
        get: operations["getCampaign"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/segment": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Generate the segment rule from the stored brief with the LLM (REQ-AUD-00..04, REQ-AI-01)
         * @description Non-streaming. LLM tool `emit_segment_rule` -> segment.Parse + segment.Validate -> at most one
         *     repair attempt -> audience count (consent filtered per channel) at the reference time
         *     (SEGMENT_REFERENCE_TIME or now). Also stores `longest_values` (content.Longest over the matched
         *     set) used by content validation. Request body: none or `{}` (any property => 422
         *     `schema_violation`; non-JSON => 400; > 65536 bytes => 413).
         *
         *     Allowed states: draft, audience_ready, content_ready, approved. On success the state becomes
         *     `audience_ready`, `segment.revision` increments, any active approval is revoked
         *     (`segment_changed`) and `latest_content_version` becomes null (older versions are stale).
         *     On any error the campaign (state, previous segment) is unchanged.
         */
        post: operations["generateSegment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/segments/count": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Validate an explicit rule and count its audience (REQ-AUD-01, REQ-AUD-02, REQ-AUD-03; VARSAYIM)
         * @description Body follows schemas/segment-rule.schema.json (`{"rule": {...}}`). Same Parse/Validate/Compile path as
         *     LLM output; no LLM call; nothing stored. Reference time = SEGMENT_REFERENCE_TIME or now.
         *     Invalid rule => 422 with field errors (segment-dsl.md §7.3); the audience query is not executed.
         */
        post: operations["countSegment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/content": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Stream SMS + email content generation over SSE (REQ-CNT-04, REQ-AI-01)
         * @description Request body: none or `{}`. Response is `text/event-stream` with events `delta`, `invalid`,
         *     `validated` exactly as in sse-events.md §1; the stream always ends with `validated` or
         *     `invalid` with `final: true` (unless the client disconnects).
         *
         *     Allowed states: audience_ready, content_ready, approved. Errors detected before the stream
         *     opens are returned as JSON with the status codes below (no SSE); use-case order: 404
         *     `campaign_not_found`, 409 `invalid_state`, 409 `generation_in_progress`, 502 `llm_unavailable`.
         *     After HTTP 200 the equivalent of 502 is the final `invalid` event. HTTP 200 is written only
         *     after LLM attempt 1 is connected; headers: `Content-Type: text/event-stream; charset=utf-8`,
         *     `Cache-Control: no-cache`, `X-Accel-Buffering: no`. The `Accept` header is not negotiated
         *     (always SSE). The client cancels by closing the connection (no cancel endpoint).
         *     Effects: `validated` => new version (status validated, source llm), state `content_ready`,
         *     active approval revoked (`content_changed`). Final `invalid` with a storable draft => new
         *     version with status `invalid` that does NOT become `latest_content_version`, does NOT revoke
         *     an approval and does NOT change the state (VARSAYIM, sse-events.md §1.2 #6). A concurrent
         *     generation for the same campaign => 409 `generation_in_progress` (reproducible with the
         *     `stalled_stream` mock scenario).
         */
        post: operations["generateContent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/content-versions": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        /** All content versions of the campaign, newest (highest version_number) first */
        get: operations["listContentVersions"];
        put?: never;
        /**
         * Human edit — creates a new validated content version (REQ-APR-01, REQ-APR-02, REQ-APR-03)
         * @description Body follows schemas/content.schema.json (full content: sms.body, email.subject, email.body).
         *     Goes through exactly the same content.Parse + content.Validate(c, segment.longest_values) as LLM
         *     output: lengths are measured AFTER substituting every variable with the current segment's
         *     longest values (REQ-CNT-01/05). Invalid content => 422 and NOTHING is stored; field paths are
         *     `sms.body`, `email.subject`, `email.body` (a missing or unknown key is `schema_violation` at that
         *     path, an empty/whitespace-only value is `required`; at most one error per field).
         *     Optimistic concurrency (owner decision, DECISIONS D40): the REQUIRED header `X-Expected-Version`
         *     names the version the editor is based on: the id of the campaign's latest content version
         *     (`Campaign.latest_content_version.id`: highest version_number among VALIDATED versions of the
         *     current segment revision) as the client last saw it, or the literal `none` when there was none.
         *     An `invalid` draft never counts as a version here: an editor seeded from an invalid draft sends the
         *     latest validated id (or `none`). Missing header => 422 `schema_violation`, path
         *     `header.X-Expected-Version`; neither a UUID nor `none` => 422 `invalid_value`, same path (both
         *     before the body is read). If it differs from the CURRENT latest version (read under the campaign
         *     row lock, in the same transaction that inserts) => 409 `content_edit_conflict` whose body carries
         *     `error.current_version` {id, version_number} (both null when none exists); NOTHING is stored.
         *     A regeneration (`validated`) or another edit creates a new latest version, so an editor open on the
         *     old one gets this 409; a failed regeneration (`invalid` draft) does not. Retrying an edit that
         *     actually succeeded also yields 409 (its base is no longer latest): the server never de-duplicates;
         *     the client compares the current version's content with its text (ui.md §3.23).
         *     Use-case order: 404 `campaign_not_found`, 409 `generation_in_progress`, 409 `invalid_state`
         *     (draft, sending, completed), 409 `content_edit_conflict`, then content validation (422). Allowed
         *     states: audience_ready, content_ready, approved. On success, in one transaction: new version
         *     (status validated, source edit, version_number = highest existing number of ANY status + 1,
         *     never reused, not reset by a new segment revision), state `content_ready`,
         *     active approval revoked (`content_changed`). Identical text still creates a new version.
         *     Two racing edits from the same base: one succeeds, the other gets 409 `content_edit_conflict`
         *     (serialized by the campaign row lock). Existing versions are never modified.
         *     The response carries `worst_case` (post-substitution lengths) for the UI's second counter line.
         */
        post: operations["editContent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/content-versions/{version_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
                version_id: components["parameters"]["VersionId"];
            };
            cookie?: never;
        };
        /** One content version of the campaign (validated or invalid) */
        get: operations["getContentVersion"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/approve": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve a content version (REQ-APR-01, REQ-APR-03, REQ-CNT-04, REQ-CNT-05)
         * @description Checks, in this order (first failure wins):
         *     0. (before the checks below) body: unreadable/not JSON 400, > 65536 bytes 413, unknown or
         *        missing property or malformed `content_version_id` 422 (`schema_violation` / `invalid_value`);
         *     1. campaign exists, else 404 `campaign_not_found`;
         *     2. version belongs to the campaign, else 404 `content_version_not_found`;
         *     2b. no generation running for the campaign, else 409 `generation_in_progress`;
         *     3. version status is `validated`, else 409 `content_not_validated`;
         *     4. state is `content_ready` or `approved`, else 409 `invalid_state`;
         *     5. version is the latest VALIDATED version (highest version_number among validated versions)
         *        AND has the current segment revision, else 409 `stale_content_version`;
         *     6. worst-case RE-check (content.CheckWorstCase with the longest first_name (fallback
         *        included), last_name and city among customers MATCHED by the rule at the campaign's
         *        reference time, before consent filtering — owner decision), recomputed now, else 422
         *        `validation_failed` with field codes `sms_too_long_worst_case` / `subject_too_long_worst_case`.
         *        Validation already applies the same rule, so over HTTP this fails only if customer data
         *        changed after validation. Test placement: an INTEGRATION test on its OWN database (seed, then
         *        validate content, then `UPDATE customers SET first_name = <longer value> WHERE id = <matched id>`
         *        as the app role `scc_app`, then approve => 422). This mutation is FORBIDDEN in the shared
         *        seeded E2E database (it would change manifest counts for other tests).
         *     The 422 `fields[]` entries are `{path: "sms.body", code: "sms_too_long_worst_case", limit: 160,
         *     actual: n}` and/or `{path: "email.subject", code: "subject_too_long_worst_case", limit: 80,
         *     actual: n}`; nothing changes.
         *     Success, in one transaction: approval row bound to the version, `campaigns.approved_content_version_id`
         *     set, state `approved`. Approving the already approved version again (state `approved`, version
         *     is still the latest validated) is idempotent: 200 with the unchanged Campaign, no new approval row,
         *     `approved_at` unchanged, and check 6 is NOT run again. A newer version created in the meantime
         *     makes the older one 409 `stale_content_version`. Racing with editContent the outcome is
         *     sequential: approve first (then the edit revokes it) or edit first (then approve is stale).
         */
        post: operations["approveContent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/send": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Accept the send and create all deliveries atomically (REQ-SND-01, REQ-APR-01, REQ-AUD-04, REQ-SND-03)
         * @description Checks in order: 404 `campaign_not_found`; 404 `content_version_not_found`;
         *     409 `generation_in_progress` (a content generation could otherwise persist into a campaign that
         *     is already `sending`);
         *     state must be `approved`, else 409 `invalid_state` (no delivery row is created);
         *     `content_version_id` must equal the approved version, else 409 `stale_content_version`.
         *     Success, in ONE transaction: one delivery per (channel, customer) of the channel audiences
         *     (segment-dsl.md §6) evaluated at the campaign's stored reference time, status `queued`,
         *     `next_attempt_at` = now; state `sending`. If both audiences are empty the campaign becomes
         *     `completed` in the same transaction. Returns immediately (202); workers send in the background.
         *     A second send request => 409 `invalid_state`.
         */
        post: operations["sendCampaign"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/status": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        /** Delivery counters snapshot (REQ-TRK-01) */
        get: operations["getCampaignStatus"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        /**
         * Live delivery status over SSE, EventSource compatible (REQ-TRK-01)
         * @description Events `snapshot`, `status`, `completed` (sse-events.md §2).
         */
        get: operations["streamCampaignEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/deliveries": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        /**
         * Paginated deliveries (REQ-SND-01, REQ-SND-04, REQ-TRK-01)
         * @description Contains no recipient address (no phone, no email) — REQ-SEC-01 spirit.
         */
        get: operations["listDeliveries"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/campaigns/{campaign_id}/preview": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        /**
         * Render a content version for one sample customer (REQ-APR-01, REQ-CNT-03, REQ-UI-01)
         * @description Defaults: `content_version_id` = latest validated version of the current segment revision.
         *     Sample recipient: if `customer_id` is given it is used (must be matched by the rule);
         *     otherwise the `sample_index`-th customer (0-based, default 0) of the matched set ordered by
         *     customer id (UUID text order) at the campaign's reference time; `sample_index` >= matched
         *     count => 404 `customer_not_found`. The UI "next sample" control increments `sample_index`
         *     modulo `sample_count`. Errors: no segment or no validated version => 409
         *     `invalid_state`; given version not in campaign => 404 `content_version_not_found`; given
         *     version has status invalid => 409 `content_not_validated`; given customer not matched by the
         *     rule => 404 `customer_not_found`; malformed `customer_id`, `content_version_id` or negative /
         *     non-integer `sample_index` => 422 `invalid_value` (path `query.<name>`). Allowed in every state
         *     that has a segment and a validated version (including sending and completed). The render is
         *     deterministic: same query, same stored data, same output. `sms.text` and `email.subject`/`email.text`
         *     are plain text (never HTML); `email.html` is the only HTML and is escaped. `email.html` is content.RenderEmailHTML output: every
         *     template and variable fragment is HTML-escaped, it contains no element other than the
         *     fixed skeleton (no script element), and the UI shows it only in a sandboxed iframe.
         */
        get: operations["previewContent"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        Health: {
            /** @constant */
            status: "ok";
        };
        Readiness: {
            /** @enum {unknown} */
            status: "ready" | "not_ready";
            checks: {
                /** @enum {unknown} */
                database: "ok" | "unavailable";
            };
        };
        AppConfig: {
            /**
             * @description Effective provider (llm.Select result). The UI shows a "Mock LLM" badge when `mock`.
             * @enum {string}
             */
            llm_provider: "mock" | "claude";
            /** @description Present only when llm_provider is `mock`; the 14 names of llm-tools.md §3.2 in order. */
            mock_scenarios?: components["schemas"]["MockScenario"][];
            limits: {
                /** @constant */
                sms_max_runes: 160;
                /** @constant */
                email_subject_max_runes: 80;
                /** @constant */
                email_body_max_runes: 5000;
                /** @constant */
                brief_max_runes: 2000;
                /** @constant */
                request_body_max_bytes: 65536;
            };
            /** @description Exactly ["first_name", "last_name", "city"]. */
            template_variables: string[];
            /** @constant */
            first_name_fallback: "Değerli müşterimiz";
        };
        /** @enum {string} */
        MockScenario: "ok" | "invalid_json" | "schema_violation" | "over_limit_sms" | "over_limit_subject" | "unknown_variable" | "injection_attempt" | "truncated_stream" | "then_ok_after_repair" | "always_invalid" | "sql_in_value" | "html_in_content" | "llm_unavailable" | "stalled_stream";
        /** @enum {string} */
        CampaignState: "draft" | "audience_ready" | "content_ready" | "approved" | "sending" | "completed";
        /** @enum {string} */
        Channel: "sms" | "email";
        /** @enum {string} */
        DeliveryStatus: "queued" | "sending" | "sent" | "failed";
        /** @enum {string} */
        FailureReason: "provider_rejected" | "max_attempts_exceeded" | "rate_limit_budget_exhausted" | "content_too_long" | "recipient_unavailable";
        CreateCampaignRequest: {
            /** @description 1..2000 characters after trim */
            brief: string;
        };
        Audience: {
            /** @description Customers matched by the rule, consent ignored */
            matched: number;
            /** @description matched AND sms_opt_in AND phone present */
            sms: number;
            /** @description matched AND email_opt_in AND email present */
            email: number;
        };
        EmptyObject: Record<string, never>;
        /** @description Body of countSegment; the single source is schemas/segment-rule.schema.json (root object with property `rule`). */
        SegmentRuleDocument: components["schemas"]["segment-rule.schema"];
        Segment: {
            /** @description Root group, e.g. {"all":[...]} (value of the tool input property `rule`); always a valid rule. */
            rule: components["schemas"]["group1"];
            audience: components["schemas"]["Audience"];
            longest_values: components["schemas"]["LongestValues"];
            /** Format: date-time */
            reference_time: string;
            revision: number;
            /** @enum {integer} */
            llm_attempts: 1 | 2;
            /** Format: date-time */
            generated_at: string;
        };
        /** @description Longest rune lengths over customers matched by the rule (before consent); first_name includes the fallback (empty matched set => 18/0/0). */
        LongestValues: {
            first_name: number;
            last_name: number;
            city: number;
        };
        /** @description Lengths after substituting every variable with the segment's longest values (validated versions => sms_runes <= 160, subject_runes <= 80). */
        WorstCase: {
            sms_runes: number;
            subject_runes: number;
        };
        SegmentResult: {
            /** Format: uuid */
            campaign_id: string;
            state: components["schemas"]["CampaignState"];
            segment: components["schemas"]["Segment"];
        };
        SegmentCount: {
            audience: components["schemas"]["Audience"];
            /** Format: date-time */
            reference_time: string;
        };
        /** @description Body of editContent and content of validated versions; the single source is schemas/content.schema.json. */
        Content: components["schemas"]["content.schema"];
        /**
         * @description Shape of a stored version's content. Same keys as schemas/content.schema.json but values may be
         *     empty: an `invalid` LLM draft is stored when a key is present with an empty value (field code
         *     `required`, sse-events.md §1). `validated` versions additionally satisfy Content (see ContentVersion).
         */
        ContentDraft: {
            sms: {
                body: string;
            };
            email: {
                subject: string;
                body: string;
            };
        };
        ContentVersion: {
            /** Format: uuid */
            id: string;
            /** Format: uuid */
            campaign_id: string;
            /** @description Per campaign, increasing, never reused, not reset by a new segment revision; stored `invalid` drafts consume a number too (validated versions may therefore skip numbers). */
            version_number: number;
            segment_revision: number;
            /** @enum {string} */
            status: "validated" | "invalid";
            /** @enum {string} */
            source: "llm" | "edit";
            content: components["schemas"]["ContentDraft"];
            /** @description Null iff the template syntax is invalid (malformed/unknown variable). */
            worst_case: components["schemas"]["WorstCase"] | null;
            /** @description Empty iff status is validated. */
            validation_errors: components["schemas"]["FieldError"][];
            /** @enum {integer|null} */
            llm_attempts: 1 | 2 | null;
            /** Format: date-time */
            created_at: string;
        };
        /** @description Data of SSE event `delta` (sse-events.md §1.1). Valid (channel, field) pairs are (sms, body), (email, subject), (email, body). */
        ContentDeltaEvent: {
            /**
             * @description 2 = repair attempt
             * @enum {integer}
             */
            attempt: 1 | 2;
            channel: components["schemas"]["Channel"];
            /** @enum {string} */
            field: "body" | "subject";
            /** @description Non-empty chunk; concatenated in order per (attempt, channel, field). */
            text: string;
        } & unknown;
        /**
         * @description Data of SSE event `invalid` (sse-events.md §1.1). `final: false` => end of attempt 1 (attempt is 1,
         *     content_version_id is null) and the next `delta` carries attempt 2. `final: true` => terminal event;
         *     content_version_id is set only when the last draft is structurally valid and was stored with
         *     status `invalid`.
         */
        ContentInvalidEvent: {
            /** @enum {integer} */
            attempt: 1 | 2;
            final: boolean;
            errors: components["schemas"]["FieldError"][];
            /** Format: uuid */
            content_version_id: string | null;
        };
        /** @description Data of SSE event `validated` (sse-events.md §1.1), always the terminal event of a successful run; the version is already stored and the campaign is `content_ready`. */
        ContentValidatedEvent: {
            /** @enum {integer} */
            attempt: 1 | 2;
            /** Format: uuid */
            content_version_id: string;
            version_number: number;
            content: components["schemas"]["content.schema"];
            worst_case: components["schemas"]["WorstCase"];
        };
        Approval: {
            /** Format: uuid */
            id: string;
            /** Format: uuid */
            content_version_id: string;
            /** Format: date-time */
            approved_at: string;
        };
        Campaign: {
            /** Format: uuid */
            id: string;
            brief: string;
            state: components["schemas"]["CampaignState"];
            segment: components["schemas"]["Segment"] | null;
            /** @description Highest version_number among VALIDATED versions of the CURRENT segment revision; null if none. Invalid drafts never appear here. */
            latest_content_version: components["schemas"]["ContentVersion"] | null;
            /** @description Active (not revoked) approval; non-null iff state is approved, sending or completed. */
            approval: components["schemas"]["Approval"] | null;
            /** Format: date-time */
            sending_started_at: string | null;
            /** Format: date-time */
            completed_at: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
        };
        CampaignPage: {
            items: components["schemas"]["Campaign"][];
            total: number;
            limit: number;
            offset: number;
        };
        VersionRef: {
            /**
             * Format: uuid
             * @description Malformed UUID => 422 invalid_value (path content_version_id)
             */
            content_version_id: string;
        };
        SendResult: {
            /** Format: uuid */
            campaign_id: string;
            /** @enum {string} */
            state: "sending" | "completed";
            /** Format: uuid */
            content_version_id: string;
            deliveries: {
                total: number;
                /** @description Equals segment.audience.sms */
                sms: number;
                /** @description Equals segment.audience.email */
                email: number;
            };
        };
        StatusCounts: {
            total: number;
            queued: number;
            sending: number;
            sent: number;
            failed: number;
        };
        CampaignStatus: {
            /** Format: uuid */
            campaign_id: string;
            state: components["schemas"]["CampaignState"];
            counts: components["schemas"]["StatusCounts"];
            by_channel: {
                sms: components["schemas"]["StatusCounts"];
                email: components["schemas"]["StatusCounts"];
            };
            /** Format: date-time */
            sending_started_at: string | null;
            /** Format: date-time */
            completed_at: string | null;
            /** Format: date-time */
            updated_at: string;
        };
        Delivery: {
            /**
             * Format: uuid
             * @description Also the Idempotency-Key sent to the provider
             */
            id: string;
            /** Format: uuid */
            campaign_id: string;
            /** Format: uuid */
            content_version_id: string;
            /** Format: uuid */
            customer_id: string;
            channel: components["schemas"]["Channel"];
            status: components["schemas"]["DeliveryStatus"];
            attempts: number;
            failed_attempts: number;
            rate_limited_count: number;
            retry_count: number;
            /** Format: date-time */
            next_attempt_at: string;
            /** Format: date-time */
            first_attempt_at: string | null;
            /** Format: date-time */
            last_attempt_at: string | null;
            last_http_status: number | null;
            last_error: string | null;
            failure_reason: components["schemas"]["FailureReason"] | null;
            provider_message_id: string | null;
            /** Format: date-time */
            sent_at: string | null;
            /** Format: date-time */
            failed_at: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
        };
        DeliveryPage: {
            items: components["schemas"]["Delivery"][];
            /** @description Count matching the filters */
            total: number;
            limit: number;
            offset: number;
        };
        Preview: {
            /** Format: uuid */
            campaign_id: string;
            /** Format: uuid */
            content_version_id: string;
            /** @description Index of the shown customer in the matched set (also when customer_id was given) */
            sample_index: number;
            /** @description Matched customer count */
            sample_count: number;
            /** @description No email, no phone. */
            customer: {
                /** Format: uuid */
                id: string;
                /** @description Raw value (may be empty) */
                first_name: string;
                last_name: string;
                city: string;
            };
            sms: {
                /** @description Rendered plain text (fallback "Değerli müşterimiz" for empty first_name) */
                text: string;
                runes: number;
                /** @constant */
                limit: 160;
                within_limit: boolean;
            };
            email: {
                /** @description Rendered plain text */
                subject: string;
                subject_runes: number;
                /** @constant */
                subject_limit: 80;
                /** @description subject_runes <= 80 */
                within_limit: boolean;
                /** @description Rendered plain-text body */
                text: string;
                /** @description content.RenderEmailHTML(subject, text): escaped, fixed skeleton */
                html: string;
            };
        };
        FieldError: {
            /** @description e.g. "brief", "sms.body", "rule.all[0].field", "header.X-Mock-LLM-Scenario", "query.limit", "" (whole document) */
            path: string;
            /** @enum {string} */
            code: "required" | "too_long" | "invalid_value" | "invalid_json" | "schema_violation" | "truncated_output" | "no_tool_call" | "llm_unavailable" | "internal_error" | "sms_too_long" | "subject_too_long" | "email_body_too_long" | "sms_too_long_worst_case" | "subject_too_long_worst_case" | "unknown_variable" | "malformed_variable" | "unknown_field" | "unknown_operator" | "operator_not_allowed" | "type_mismatch" | "empty_group" | "max_depth_exceeded" | "max_conditions_exceeded";
            message: string;
            limit?: number;
            actual?: number;
        };
        ErrorResponse: {
            error: {
                /** @enum {string} */
                code: "malformed_request" | "not_found" | "method_not_allowed" | "campaign_not_found" | "content_version_not_found" | "customer_not_found" | "invalid_state" | "content_not_validated" | "stale_content_version" | "content_edit_conflict" | "generation_in_progress" | "payload_too_large" | "validation_failed" | "llm_invalid_output" | "llm_unavailable" | "internal_error";
                /** @description English; never contains secrets, SQL or DB error text */
                message: string;
                /** @description Present (non-empty) for validation_failed and llm_invalid_output; absent otherwise (pinned per response in components.responses). */
                fields?: components["schemas"]["FieldError"][];
                /** @description Present only for `content_edit_conflict` (the CURRENT latest validated version of the campaign; both members null when it has none), so the client can offer a reload. */
                current_version?: {
                    /** Format: uuid */
                    id: string | null;
                    version_number: number | null;
                };
                /** @description Same value as the X-Request-ID response header */
                request_id: string;
            };
        };
        /** @description Yalnızca yapı denetlenir (string). İzinli adlar: city, language, loyalty_tier, last_cart_add_at, last_purchase_at, orders_30d, cart_adds_30d, total_orders, total_spent. Listede olmayan ad `segment.Validate`'te `unknown_field` olur; enum yalnızca modele giden strict varyantta (schemas/llm/) bulunur. */
        fieldName: string;
        /** @description Yalnızca yapı denetlenir (string). İzinli adlar: eq, neq, in, not_in, gt, gte, lt, lte, between, within_last, older_than, is_null, is_not_null. Listede olmayan ad `segment.Validate`'te `unknown_operator` olur; enum yalnızca modele giden strict varyantta (schemas/llm/) bulunur. */
        operator: string;
        relativeDays: {
            days: number;
        };
        range: {
            min: number;
            max: number;
        };
        value: string | number | string[] | components["schemas"]["relativeDays"] | components["schemas"]["range"] | null;
        condition: {
            field: components["schemas"]["fieldName"];
            op: components["schemas"]["operator"];
            value?: components["schemas"]["value"];
        };
        /** @description Deepest group level: children are conditions only. */
        group3: {
            all: components["schemas"]["condition"][];
        } | {
            any: components["schemas"]["condition"][];
        } | {
            not: components["schemas"]["condition"];
        };
        group2: {
            all: (components["schemas"]["condition"] | components["schemas"]["group3"])[];
        } | {
            any: (components["schemas"]["condition"] | components["schemas"]["group3"])[];
        } | {
            not: components["schemas"]["condition"] | components["schemas"]["group3"];
        };
        /** @description Root group (depth 1). The root must be a group, never a bare condition. */
        group1: {
            all: (components["schemas"]["condition"] | components["schemas"]["group2"])[];
        } | {
            any: (components["schemas"]["condition"] | components["schemas"]["group2"])[];
        } | {
            not: components["schemas"]["condition"] | components["schemas"]["group2"];
        };
        /**
         * CampaignContent
         * @description Input of the emit_campaign_content tool AND request body of POST /api/campaigns/{campaign_id}/content-versions (human edit). Both go through the same backend validator (content.Validate). Length limits (sms.body <= 160, email.subject <= 80, email.body <= 5000 Unicode code points of the template text) and template-variable rules are NOT expressed as maxLength/pattern here on purpose: the backend reports them with specific field error codes (sms_too_long, subject_too_long, email_body_too_long, unknown_variable, malformed_variable). `minLength: 1` documents that empty is invalid, but content.Parse checks structure and types only: an empty or whitespace-only value is reported by content.Validate as field code `required` (never schema_violation), a missing key or unknown key as schema_violation. See llm-tools.md and go-interfaces.md#content.
         */
        "content.schema": {
            sms: {
                /** @description Plain-text SMS template. At most 160 Unicode characters after personalization. Allowed variables: {{first_name}}, {{last_name}}, {{city}}. */
                body: string;
            };
            email: {
                /** @description Plain-text email subject template. At most 80 Unicode characters after personalization. */
                subject: string;
                /** @description Plain-text email body template (no HTML; blank line = new paragraph). The backend renders it to escaped HTML. At most 5000 Unicode characters. */
                body: string;
            };
        };
        /**
         * SegmentRuleToolInput
         * @description Input of the emit_segment_rule tool and body of POST /api/segments/count. Root is an object with a single 'rule' property. The rule tree is unrolled to depth 3 (group1 > group2 > group3); no recursion. Semantic limits that JSON Schema cannot express (max 10 conditions in total, operator-per-type table, value ranges, enum members) are enforced by the backend validator, see segment-dsl.md.
         */
        "segment-rule.schema": {
            rule: components["schemas"]["group1"];
            $defs: {
                /** @description Yalnızca yapı denetlenir (string). İzinli adlar: city, language, loyalty_tier, last_cart_add_at, last_purchase_at, orders_30d, cart_adds_30d, total_orders, total_spent. Listede olmayan ad `segment.Validate`'te `unknown_field` olur; enum yalnızca modele giden strict varyantta (schemas/llm/) bulunur. */
                fieldName: string;
                /** @description Yalnızca yapı denetlenir (string). İzinli adlar: eq, neq, in, not_in, gt, gte, lt, lte, between, within_last, older_than, is_null, is_not_null. Listede olmayan ad `segment.Validate`'te `unknown_operator` olur; enum yalnızca modele giden strict varyantta (schemas/llm/) bulunur. */
                operator: string;
                relativeDays: {
                    days: number;
                };
                range: {
                    min: number;
                    max: number;
                };
                value: string | number | string[] | components["schemas"]["relativeDays"] | components["schemas"]["range"] | null;
                condition: {
                    field: components["schemas"]["fieldName"];
                    op: components["schemas"]["operator"];
                    value?: components["schemas"]["value"];
                };
                /** @description Deepest group level: children are conditions only. */
                group3: {
                    all: components["schemas"]["condition"][];
                } | {
                    any: components["schemas"]["condition"][];
                } | {
                    not: components["schemas"]["condition"];
                };
                group2: {
                    all: (components["schemas"]["condition"] | components["schemas"]["group3"])[];
                } | {
                    any: (components["schemas"]["condition"] | components["schemas"]["group3"])[];
                } | {
                    not: components["schemas"]["condition"] | components["schemas"]["group3"];
                };
                /** @description Root group (depth 1). The root must be a group, never a bare condition. */
                group1: {
                    all: (components["schemas"]["condition"] | components["schemas"]["group2"])[];
                } | {
                    any: (components["schemas"]["condition"] | components["schemas"]["group2"])[];
                } | {
                    not: components["schemas"]["condition"] | components["schemas"]["group2"];
                };
            };
        };
    };
    responses: {
        /** @description `malformed_request` — body is not valid JSON or Content-Type is not application/json. */
        MalformedRequest: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code?: "malformed_request";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `campaign_not_found` — unknown campaign (malformed UUID included). */
        NotFound: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code?: "campaign_not_found";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `campaign_not_found` or `content_version_not_found` (version missing or belongs to another campaign). */
        VersionNotFound: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @enum {unknown} */
                        code?: "campaign_not_found" | "content_version_not_found";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `campaign_not_found`, `content_version_not_found` or `customer_not_found`. */
        PreviewNotFound: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @enum {unknown} */
                        code?: "campaign_not_found" | "content_version_not_found" | "customer_not_found";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `invalid_state`, `content_not_validated`, `stale_content_version` or `generation_in_progress` (approveContent order of checks). */
        Conflict: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @enum {unknown} */
                        code?: "invalid_state" | "content_not_validated" | "stale_content_version" | "generation_in_progress";
                        fields?: unknown;
                    };
                };
            };
        };
        /**
         * @description editContent 409: `invalid_state`, `generation_in_progress`, or `content_edit_conflict` (stale
         *     `X-Expected-Version`); the last one carries `error.current_version`.
         */
        EditConflict: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @enum {unknown} */
                        code: "invalid_state" | "generation_in_progress";
                        fields?: unknown;
                        current_version?: unknown;
                    } | {
                        /** @constant */
                        code: "content_edit_conflict";
                        fields?: unknown;
                        current_version: Record<string, never>;
                    };
                };
            };
        };
        /** @description `invalid_state` or `generation_in_progress`. */
        GenerationConflict: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @enum {unknown} */
                        code?: "invalid_state" | "generation_in_progress";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `invalid_state` (not approved, second send), `stale_content_version` or `generation_in_progress`. */
        SendConflict: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @enum {unknown} */
                        code?: "invalid_state" | "stale_content_version" | "generation_in_progress";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `invalid_state` (no segment or no validated version) or `content_not_validated`. */
        PreviewConflict: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @enum {unknown} */
                        code?: "invalid_state" | "content_not_validated";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `payload_too_large` — body larger than 65536 bytes. */
        PayloadTooLarge: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code?: "payload_too_large";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `validation_failed` with at least one entry in `error.fields` (example body: README.md §5). */
        ValidationFailed: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code?: "validation_failed";
                        fields: unknown;
                    };
                };
            };
        };
        /**
         * @description `llm_invalid_output` (output still invalid after the single repair attempt; `error.fields`
         *     holds the last attempt's errors) or `llm_unavailable`. The response never contains database
         *     error text, the API key, or the raw model output.
         */
        BadGateway: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code: "llm_invalid_output";
                        fields: unknown;
                    } | {
                        /** @constant */
                        code: "llm_unavailable";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `internal_error` — unexpected failure (panic recovery included); message is fixed, no internal detail. */
        InternalError: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code?: "internal_error";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `not_found` — no path template matches the request path. */
        RouteNotFound: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code?: "not_found";
                        fields?: unknown;
                    };
                };
            };
        };
        /** @description `method_not_allowed` — the path exists but does not define this method. */
        MethodNotAllowed: {
            headers: {
                /** @description Comma-separated methods the path defines, sorted (e.g. `GET, POST`). */
                Allow: string;
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["ErrorResponse"] & {
                    error?: {
                        /** @constant */
                        code?: "method_not_allowed";
                        fields?: unknown;
                    };
                };
            };
        };
    };
    parameters: {
        CampaignId: string;
        VersionId: string;
        /** @description 1..200, default 50. Out of range => 422 invalid_value (path query.limit). */
        Limit: number;
        /** @description >= 0, default 0. Negative => 422 invalid_value (path query.offset). */
        Offset: number;
        /**
         * @description editContent precondition: id of the latest content version the editor is based on, or `none`
         *     (see editContent). Missing => 422 `schema_violation`; malformed => 422 `invalid_value`
         *     (path `header.X-Expected-Version`); mismatch => 409 `content_edit_conflict`.
         */
        ExpectedVersion: string;
        /**
         * @description Honored ONLY when the effective LLM provider is `mock`; ignored when `claude`.
         *     Unknown value in mock mode => 422 validation_failed, field path `header.X-Mock-LLM-Scenario`,
         *     code `invalid_value`; the LLM is not called. Default: MOCK_LLM_SCENARIO (default `ok`).
         */
        MockScenario: components["schemas"]["MockScenario"];
    };
    requestBodies: {
        /** @description None or `{}`. Any property => 422 `schema_violation`. */
        EmptyBody: {
            content: {
                "application/json": components["schemas"]["EmptyObject"];
            };
        };
    };
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    getHealthz: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Process is up. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    /**
                     * @example {
                     *       "status": "ok"
                     *     }
                     */
                    "application/json": components["schemas"]["Health"];
                };
            };
            500: components["responses"]["InternalError"];
        };
    };
    getReadyz: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Database ping succeeded within 1s. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    /**
                     * @example {
                     *       "status": "ready",
                     *       "checks": {
                     *         "database": "ok"
                     *       }
                     *     }
                     */
                    "application/json": components["schemas"]["Readiness"];
                };
            };
            500: components["responses"]["InternalError"];
            /** @description Database unreachable. */
            503: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    /**
                     * @example {
                     *       "status": "not_ready",
                     *       "checks": {
                     *         "database": "unavailable"
                     *       }
                     *     }
                     */
                    "application/json": components["schemas"]["Readiness"];
                };
            };
        };
    };
    getMetrics: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Prometheus text exposition format. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/plain": string;
                };
            };
            500: components["responses"]["InternalError"];
        };
    };
    getConfig: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. Never contains secrets. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AppConfig"];
                };
            };
            500: components["responses"]["InternalError"];
        };
    };
    getApiDocs: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The Swagger UI page. */
            200: {
                headers: {
                    "X-Content-Type-Options": "nosniff";
                    [name: string]: unknown;
                };
                content: {
                    "text/html": string;
                };
            };
            500: components["responses"]["InternalError"];
        };
    };
    getApiDocsAsset: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description One of the vendored Swagger UI files (swagger-ui-dist, Apache-2.0) or the page initializer. */
                asset: "swagger-ui-bundle.js" | "swagger-ui-bundle.js.LICENSE.txt" | "swagger-ui.css" | "swagger-initializer.js" | "favicon-32x32.png" | "LICENSE" | "NOTICE";
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The file. */
            200: {
                headers: {
                    "X-Content-Type-Options": "nosniff";
                    [name: string]: unknown;
                };
                content: {
                    "text/javascript": string;
                    "text/css": string;
                    "text/plain": string;
                    "image/png": string;
                };
            };
            404: components["responses"]["RouteNotFound"];
            500: components["responses"]["InternalError"];
        };
    };
    getOpenApiSpec: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The contract as YAML. */
            200: {
                headers: {
                    "X-Content-Type-Options": "nosniff";
                    [name: string]: unknown;
                };
                content: {
                    "application/yaml": string;
                };
            };
            500: components["responses"]["InternalError"];
        };
    };
    getContractSchema: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A JSON Schema file referenced by this contract. */
                schema: "segment-rule.schema.json" | "content.schema.json";
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The schema. */
            200: {
                headers: {
                    "X-Content-Type-Options": "nosniff";
                    [name: string]: unknown;
                };
                content: {
                    "application/schema+json": Record<string, never>;
                };
            };
            404: components["responses"]["RouteNotFound"];
            500: components["responses"]["InternalError"];
        };
    };
    listCampaigns: {
        parameters: {
            query?: {
                /** @description 1..200, default 50. Out of range => 422 invalid_value (path query.limit). */
                limit?: components["parameters"]["Limit"];
                /** @description >= 0, default 0. Negative => 422 invalid_value (path query.offset). */
                offset?: components["parameters"]["Offset"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CampaignPage"];
                };
            };
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
    createCampaign: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["CreateCampaignRequest"];
            };
        };
        responses: {
            /** @description Created. */
            201: {
                headers: {
                    /** @description /api/campaigns/{campaign_id} */
                    Location: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Campaign"];
                };
            };
            400: components["responses"]["MalformedRequest"];
            413: components["responses"]["PayloadTooLarge"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
    getCampaign: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Campaign"];
                };
            };
            404: components["responses"]["NotFound"];
            500: components["responses"]["InternalError"];
        };
    };
    generateSegment: {
        parameters: {
            query?: never;
            header?: {
                /**
                 * @description Honored ONLY when the effective LLM provider is `mock`; ignored when `claude`.
                 *     Unknown value in mock mode => 422 validation_failed, field path `header.X-Mock-LLM-Scenario`,
                 *     code `invalid_value`; the LLM is not called. Default: MOCK_LLM_SCENARIO (default `ok`).
                 */
                "X-Mock-LLM-Scenario"?: components["parameters"]["MockScenario"];
            };
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: components["requestBodies"]["EmptyBody"];
        responses: {
            /** @description Valid rule stored and counted. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SegmentResult"];
                };
            };
            400: components["responses"]["MalformedRequest"];
            404: components["responses"]["NotFound"];
            /** @description `invalid_state` (sending/completed) or `generation_in_progress` (another segment/content generation for this campaign is running). */
            409: components["responses"]["GenerationConflict"];
            413: components["responses"]["PayloadTooLarge"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
            502: components["responses"]["BadGateway"];
        };
    };
    countSegment: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["segment-rule.schema"];
            };
        };
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SegmentCount"];
                };
            };
            400: components["responses"]["MalformedRequest"];
            413: components["responses"]["PayloadTooLarge"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
    generateContent: {
        parameters: {
            query?: never;
            header?: {
                /**
                 * @description Honored ONLY when the effective LLM provider is `mock`; ignored when `claude`.
                 *     Unknown value in mock mode => 422 validation_failed, field path `header.X-Mock-LLM-Scenario`,
                 *     code `invalid_value`; the LLM is not called. Default: MOCK_LLM_SCENARIO (default `ok`).
                 */
                "X-Mock-LLM-Scenario"?: components["parameters"]["MockScenario"];
            };
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: components["requestBodies"]["EmptyBody"];
        responses: {
            /** @description SSE stream (see sse-events.md §1). The body is not validated as a whole (x-sse-events); every event's `data` JSON validates against the schema named in `x-sse-events.schemas`. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": string;
                };
            };
            400: components["responses"]["MalformedRequest"];
            404: components["responses"]["NotFound"];
            /** @description `invalid_state` (draft, sending, completed) or `generation_in_progress` (lock released on client disconnect or after 120 s). */
            409: components["responses"]["GenerationConflict"];
            413: components["responses"]["PayloadTooLarge"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
            /** @description `llm_unavailable` before the stream opens (after HTTP 200 the equivalent is the final `invalid` event). */
            502: components["responses"]["BadGateway"];
        };
    };
    listContentVersions: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["ContentVersion"][];
                    };
                };
            };
            404: components["responses"]["NotFound"];
            500: components["responses"]["InternalError"];
        };
    };
    editContent: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description editContent precondition: id of the latest content version the editor is based on, or `none`
                 *     (see editContent). Missing => 422 `schema_violation`; malformed => 422 `invalid_value`
                 *     (path `header.X-Expected-Version`); mismatch => 409 `content_edit_conflict`.
                 */
                "X-Expected-Version": components["parameters"]["ExpectedVersion"];
            };
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["content.schema"];
            };
        };
        responses: {
            /** @description Created. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContentVersion"];
                };
            };
            400: components["responses"]["MalformedRequest"];
            404: components["responses"]["NotFound"];
            409: components["responses"]["EditConflict"];
            413: components["responses"]["PayloadTooLarge"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
    getContentVersion: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
                version_id: components["parameters"]["VersionId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContentVersion"];
                };
            };
            404: components["responses"]["VersionNotFound"];
            500: components["responses"]["InternalError"];
        };
    };
    approveContent: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["VersionRef"];
            };
        };
        responses: {
            /** @description Approved. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Campaign"];
                };
            };
            400: components["responses"]["MalformedRequest"];
            404: components["responses"]["VersionNotFound"];
            409: components["responses"]["Conflict"];
            413: components["responses"]["PayloadTooLarge"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
    sendCampaign: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["VersionRef"];
            };
        };
        responses: {
            /** @description Accepted. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SendResult"];
                };
            };
            400: components["responses"]["MalformedRequest"];
            404: components["responses"]["VersionNotFound"];
            409: components["responses"]["SendConflict"];
            413: components["responses"]["PayloadTooLarge"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
    getCampaignStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CampaignStatus"];
                };
            };
            404: components["responses"]["NotFound"];
            500: components["responses"]["InternalError"];
        };
    };
    streamCampaignEvents: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description SSE stream (see sse-events.md §2). The body is not validated as a whole (x-sse-events); every event's `data` JSON validates against CampaignStatus. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": string;
                };
            };
            404: components["responses"]["NotFound"];
            500: components["responses"]["InternalError"];
        };
    };
    listDeliveries: {
        parameters: {
            query?: {
                /** @description Unknown value => 422 invalid_value (path query.status). */
                status?: components["schemas"]["DeliveryStatus"];
                /** @description Unknown value => 422 invalid_value (path query.channel). */
                channel?: components["schemas"]["Channel"];
                /** @description `customer` = customer_id ASC, channel ASC (default); `recent` = updated_at DESC, id ASC (used by the UI activity feed). Unknown value => 422 invalid_value (path query.order). */
                order?: "customer" | "recent";
                /** @description Only this customer's deliveries (0..2 rows). Malformed UUID => 422 invalid_value (path query.customer_id). */
                customer_id?: string;
                /** @description 1..200, default 50. Out of range => 422 invalid_value (path query.limit). */
                limit?: components["parameters"]["Limit"];
                /** @description >= 0, default 0. Negative => 422 invalid_value (path query.offset). */
                offset?: components["parameters"]["Offset"];
            };
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DeliveryPage"];
                };
            };
            404: components["responses"]["NotFound"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
    previewContent: {
        parameters: {
            query?: {
                customer_id?: string;
                content_version_id?: string;
                /** @description >= 0, default 0. Negative => 422 invalid_value (path query.sample_index). */
                sample_index?: number;
            };
            header?: never;
            path: {
                campaign_id: components["parameters"]["CampaignId"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description OK. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Preview"];
                };
            };
            404: components["responses"]["PreviewNotFound"];
            409: components["responses"]["PreviewConflict"];
            422: components["responses"]["ValidationFailed"];
            500: components["responses"]["InternalError"];
        };
    };
}
