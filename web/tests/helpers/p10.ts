// Shared P10 test data and fakes for the brief-page campaign panel (docs/requirements.md REQ-DSH-02).
// Not a spec file.
//
// REQ-DSH-02 C1/C2: the page is `@/pages/BriefPage.vue` on route `brief-new` (`/`) and, read-only,
// on route `brief` (`/campaigns/:campaignId/brief`); the panel requests go through the client given
// with `ApiClientKey`: `request('GET', '/api/campaign-summaries', { query })`.
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { RouterView, createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiClientKey, EventSourceFactoryKey } from '@/api/client'
import BriefPage from '@/pages/BriefPage.vue'
import { CAMPAIGN_ID, apiError, campaign, fakeApi, type RecordedRequest } from './p4'
import Stub from './StubPage'

export const SUMMARIES_PATH = '/api/campaign-summaries'

export const SCHEDULED_QUERY = { state: 'scheduled', order: 'scheduled_at', limit: 5, offset: 0 }
export const SENT_QUERY = { state: 'sending,completed', order: 'recent', limit: 5, offset: 0 }

export type SummaryQuery = { state: string; order: string; limit: number; offset: number }

/** A lower-case UUID numbered n (stable ids for rows). */
export function sid(n: number): string {
  return `5e1a0c00-0000-4000-8000-${String(n).padStart(12, '0')}`
}

export interface Deliveries {
  total: number
  queued: number
  sending: number
  sent: number
  failed: number
}

export function deliveries(
  queued: number,
  sending: number,
  sent: number,
  failed: number,
): Deliveries {
  return { total: queued + sending + sent + failed, queued, sending, sent, failed }
}

/** A `CampaignSummary` item of REQ-DSH-01 C2. */
export function summary(n: number, state: string, extra: Record<string, unknown> = {}) {
  const sendingOrDone = state === 'sending' || state === 'completed'
  return {
    id: sid(n),
    brief: `Kampanya ${n}: İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder.`,
    state,
    audience: state === 'draft' ? null : { matched: 584, sms: 350, email: 368 },
    scheduled_at: null as string | null,
    sending_started_at: sendingOrDone ? '2026-01-15T09:15:04Z' : null,
    completed_at: state === 'completed' ? '2026-01-15T09:15:21Z' : null,
    created_at: '2026-01-15T09:12:40Z',
    updated_at: '2026-01-15T09:15:21Z',
    deliveries: sendingOrDone ? deliveries(0, 0, 689, 14) : null,
    ...extra,
  }
}

export type Summary = ReturnType<typeof summary>

export interface Overview {
  campaigns: Record<string, number>
  deliveries: Deliveries
}

export function overview(
  c: Partial<
    Record<
      | 'draft'
      | 'audience_ready'
      | 'content_ready'
      | 'approved'
      | 'scheduled'
      | 'sending'
      | 'completed',
      number
    >
  > = {},
  d: Deliveries = deliveries(0, 0, 2061, 43),
): Overview {
  const campaigns = {
    draft: 2,
    audience_ready: 1,
    content_ready: 1,
    approved: 0,
    scheduled: 2,
    sending: 0,
    completed: 3,
    ...c,
  }
  const total = Object.values(campaigns).reduce((a, b) => a + b, 0)
  return { campaigns: { total, ...campaigns }, deliveries: d }
}

/** A `CampaignSummaryPage` (REQ-DSH-01 C1). */
export function page(
  items: Summary[],
  total = items.length,
  ov: Overview = overview(),
  offset = 0,
) {
  return { items, total, limit: 5, offset, overview: ov }
}

export function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

/** A summaries answer; a handler that ignores the query (`() => …`) is one too. */
export type Answer = (q: SummaryQuery) => unknown

/** Answers in turn; the last answer repeats. An answer may return a value, a promise, or throw. */
export function seq(...answers: Answer[]): Answer {
  let i = 0
  return (q) => {
    const a = answers[Math.min(i, answers.length - 1)]
    i++
    return a!(q)
  }
}

/**
 * A failing answer. It takes no argument, so it fits both an `Answer` and a `routes` handler
 * (`(r: RecordedRequest) => unknown`).
 */
export const fail =
  (status = 500, code = 'internal_error') =>
  (): never => {
    throw apiError(status, code)
  }

/**
 * Fake ApiClient for the panel: GET /api/campaign-summaries is answered by `scheduled` or `sent`
 * according to `opts.query.state`; `routes` adds other requests in the fakeApi key format
 * ("METHOD /campaigns/..."). `calls` keeps the raw method, path and opts of every request.
 */
export function panelApi(h: {
  scheduled: Answer
  sent: Answer
  routes?: Record<string, (r: RecordedRequest) => unknown>
}) {
  const inner = fakeApi({
    'GET /campaign-summaries': (rec) => {
      const q = rec.opts?.query as unknown as SummaryQuery
      return q?.state === 'scheduled' ? h.scheduled(q) : h.sent(q)
    },
    ...(h.routes ?? {}),
  })
  const calls: RecordedRequest[] = []
  const client = {
    async request<T>(method: string, path: string, opts?: RecordedRequest['opts']): Promise<T> {
      calls.push({ method, path, opts })
      return inner.client.request<T>(method, path, opts)
    },
  }
  const isSummaries = (c: RecordedRequest) =>
    (c.path.split('?')[0] ?? '').endsWith('/campaign-summaries')
  return {
    client,
    calls,
    summaryCalls: () => calls.filter(isSummaries),
    summaryQueries: () => calls.filter(isSummaries).map((c) => c.opts?.query),
    count: (method: string, pathSuffix: string) =>
      calls.filter(
        (c) =>
          c.method.toUpperCase() === method && (c.path.split('?')[0] ?? '').endsWith(pathSuffix),
      ).length,
  }
}

export type PanelApi = ReturnType<typeof panelApi>

/** Default routes for the brief submission (REQ-DSH-02 C3). */
export function briefRoutes(over: Record<string, (r: RecordedRequest) => unknown> = {}) {
  return {
    'POST /campaigns': () => campaign('draft', null),
    [`POST /campaigns/${CAMPAIGN_ID}/segment`]: () => ({
      campaign_id: CAMPAIGN_ID,
      state: 'audience_ready',
    }),
    [`GET /campaigns/${CAMPAIGN_ID}`]: () => campaign('draft', null),
    ...over,
  }
}

/** Mounts BriefPage on `brief-new` (`/`) or `brief` (`/campaigns/:campaignId/brief`). */
export async function mountBrief(
  api: unknown,
  opts: { route?: 'brief-new' | 'brief'; eventSource?: unknown } = {},
): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'brief-new', component: BriefPage },
      { path: '/campaigns/:campaignId', name: 'campaign', component: Stub },
      { path: '/campaigns/:campaignId/brief', name: 'brief', component: BriefPage, props: true },
      { path: '/campaigns/:campaignId/audience', name: 'audience', component: Stub, props: true },
      { path: '/campaigns/:campaignId/content', name: 'content', component: Stub, props: true },
      { path: '/campaigns/:campaignId/approval', name: 'approval', component: Stub, props: true },
      { path: '/campaigns/:campaignId/tracking', name: 'tracking', component: Stub, props: true },
      { path: '/:pathMatch(.*)*', name: 'not-found', component: Stub },
    ],
  })
  if (opts.route === 'brief')
    await router.push({ name: 'brief', params: { campaignId: CAMPAIGN_ID } })
  else await router.push({ name: 'brief-new' })
  await router.isReady()
  const provide: Record<symbol, unknown> = { [ApiClientKey as symbol]: api }
  if (opts.eventSource) provide[EventSourceFactoryKey as symbol] = opts.eventSource
  const wrapper = mount(RouterView, {
    global: { plugins: [router, createPinia()], provide },
    attachTo: document.body,
  })
  await flushPromises()
  return { wrapper, router }
}
