// Shared P4 test data and fakes (docs/contracts/ui.md §0, §3, §5). Not a spec file.
import type { ApiError } from '@/api/errors'
import type { Campaign, Content, ContentVersion, FieldError, Preview } from '@/api/types'

export const sel = (id: string) => `[data-testid="${id}"]`

/** XSS payload of ui.md §6.1. */
export const P = '<script>alert(1)</script><b>x</b>'

export const CAMPAIGN_ID = '6b0e1f8a-3c2d-4e5f-8a9b-0c1d2e3f4a5b'
export const V1 = '0b8f6c2e-6d0a-4a47-9c39-0f2d3b8e1a11'
export const V2 = '5c1d7e3a-2b4f-4c6d-8e9f-0a1b2c3d4e5f'
export const V3 = '9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d'
export const T0 = '2026-01-15T12:00:00Z'

// CNT_OK of llm-tools.md §3.3.
export const OK_SMS =
  'Merhaba {{first_name}}, sepetindeki ürünler seni bekliyor! Bugüne özel %30 indirimle alışverişini tamamla: example.com/sepet'
export const OK_SUBJECT = 'Sepetin seni bekliyor: %30 indirim fırsatı'
export const OK_BODY =
  'Merhaba {{first_name}},\n\nSepetine eklediğin ürünler hâlâ seni bekliyor. {{city}} için bugüne özel %30 indirim tanımladık.\n\nAlışverişini tamamlamak için example.com/sepet adresini ziyaret et.\n\nSevgiler,\nSmart Campaign Composer'

export function content(sms = OK_SMS, subject = OK_SUBJECT, body = OK_BODY): Content {
  return { sms: { body: sms }, email: { subject, body } } as Content
}

export function version(
  id = V1,
  n = 1,
  c: Content = content(),
  extra: Partial<ContentVersion> = {},
): ContentVersion {
  return {
    id,
    campaign_id: CAMPAIGN_ID,
    version_number: n,
    segment_revision: 1,
    status: 'validated',
    source: 'llm',
    content: c,
    worst_case: { sms_runes: 160, subject_runes: 42 },
    validation_errors: [],
    llm_attempts: 1,
    created_at: T0,
    ...extra,
  } as ContentVersion
}

export function campaign(
  state: string,
  latest: ContentVersion | null,
  extra: Partial<Campaign> = {},
): Campaign {
  const approved = state === 'approved' || state === 'sending' || state === 'completed'
  return {
    id: CAMPAIGN_ID,
    brief:
      "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder.",
    state,
    segment:
      state === 'draft'
        ? null
        : {
            rule: { all: [{ field: 'city', op: 'eq', value: 'İstanbul' }] },
            audience: { matched: 584, sms: 350, email: 368 },
            longest_values: { first_name: 50, last_name: 8, city: 8 },
            reference_time: T0,
            revision: 1,
            llm_attempts: 1,
            generated_at: T0,
          },
    latest_content_version: latest,
    approval:
      approved && latest ? { id: V3, content_version_id: latest.id, approved_at: T0 } : null,
    sending_started_at: state === 'sending' || state === 'completed' ? T0 : null,
    completed_at: state === 'completed' ? T0 : null,
    created_at: T0,
    updated_at: T0,
    ...extra,
  } as Campaign
}

export function preview(extra: Partial<Preview> = {}): Preview {
  return {
    campaign_id: CAMPAIGN_ID,
    content_version_id: V1,
    sample_index: 0,
    sample_count: 584,
    customer: {
      id: '2b34d38c-486c-53d2-8fba-c448b990e359',
      first_name: 'Ayşe',
      last_name: 'Kaya',
      city: 'İstanbul',
    },
    sms: {
      text: 'Merhaba Ayşe, sepetindeki ürünler seni bekliyor!',
      runes: 48,
      limit: 160,
      within_limit: true,
    },
    email: {
      subject: OK_SUBJECT,
      subject_runes: 42,
      subject_limit: 80,
      within_limit: true,
      text: 'Merhaba Ayşe,\n\nİndirim.',
      html: '<!doctype html><html lang="tr"><head><meta charset="utf-8"><title>Sepetin</title></head><body><p>Merhaba Ayşe,</p><p>&lt;script&gt;</p></body></html>',
    },
    ...extra,
  } as Preview
}

export function apiError(
  status: number,
  code: string,
  fields: FieldError[] = [],
  currentVersion: ApiError['currentVersion'] = null,
): ApiError {
  return {
    status,
    code: code as ApiError['code'],
    message: 'x',
    fields,
    requestId: 'req-1',
    currentVersion,
  }
}

export function fieldError(
  path: string,
  code: string,
  limit?: number,
  actual?: number,
): FieldError {
  const fe: Record<string, unknown> = { path, code, message: 'm' }
  if (limit !== undefined) fe.limit = limit
  if (actual !== undefined) fe.actual = actual
  return fe as unknown as FieldError
}

// ---- SSE ----

/** One SSE frame (sse-events.md §0): event line, one compact data line, blank line. */
export function frame(event: string, data: unknown): string {
  return `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`
}

export const delta = (attempt: number, channel: string, field: string, text: string) =>
  frame('delta', { attempt, channel, field, text })

export const validated = (
  c: Content,
  id = V1,
  n = 1,
  attempt = 1,
  wc = { sms_runes: 160, subject_runes: 42 },
) =>
  frame('validated', {
    attempt,
    content_version_id: id,
    version_number: n,
    content: c,
    worst_case: wc,
  })

export const invalid = (
  attempt: number,
  final: boolean,
  errors: FieldError[],
  id: string | null = null,
) => frame('invalid', { attempt, final, errors, content_version_id: id })

/** A ReadableStream the test feeds chunk by chunk. */
export function controllableStream() {
  const enc = new TextEncoder()
  let ctrl!: ReadableStreamDefaultController<Uint8Array>
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      ctrl = c
    },
  })
  return {
    stream,
    push(s: string) {
      ctrl.enqueue(enc.encode(s))
    },
    close() {
      ctrl.close()
    },
    error(e: unknown = new TypeError('network error')) {
      ctrl.error(e)
    },
  }
}

export function sseResponse(stream: ReadableStream<Uint8Array>): Response {
  return new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
  })
}

export function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

export function errorBody(code: string, extra: Record<string, unknown> = {}) {
  return { error: { code, message: 'x', request_id: 'req-1', ...extra } }
}

// ---- fake ApiClient (ui.md §0: request(method, path, opts); non-2xx throws ApiError) ----

export interface RecordedRequest {
  method: string
  path: string
  opts?: {
    body?: unknown
    query?: Record<string, string | number>
    headers?: Record<string, string>
  }
}

type Handler = (req: RecordedRequest) => unknown

/**
 * Routes are keyed "METHOD /campaigns/{id}/..." without the /api prefix and without a query string;
 * the fake accepts paths with or without "/api" (ui.md does not fix the client's base path).
 * A handler returns the body or throws an ApiError object.
 */
export function fakeApi(routes: Record<string, Handler>) {
  const calls: RecordedRequest[] = []
  const norm = (p: string) => {
    const noQuery = p.split('?')[0] ?? p
    return noQuery.startsWith('/api/') ? noQuery.slice(4) : noQuery
  }
  const client = {
    async request<T>(method: string, path: string, opts?: RecordedRequest['opts']): Promise<T> {
      const rec = { method: method.toUpperCase(), path: norm(path), opts }
      calls.push(rec)
      const h = routes[`${rec.method} ${rec.path}`]
      if (!h) throw apiError(404, 'not_found')
      return (await h(rec)) as T
    },
  }
  return {
    client,
    calls,
    count(method: string, path: string) {
      return calls.filter((c) => c.method === method && c.path === path).length
    },
    last(method: string, path: string) {
      return [...calls].reverse().find((c) => c.method === method && c.path === path)
    },
  }
}

/** Case-insensitive header lookup on a recorded request. */
export function header(req: RecordedRequest | undefined, name: string): string | undefined {
  const h = req?.opts?.headers ?? {}
  const k = Object.keys(h).find((x) => x.toLowerCase() === name.toLowerCase())
  return k === undefined ? undefined : h[k]
}
