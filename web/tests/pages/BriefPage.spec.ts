// REQ-DSH-02 (UI): the campaign panel under the brief composer on `brief-new` (`/`): placement and
// visibility, the two initial requests, KPI cards, the Planlanan / Gönderilen sections and their
// rows, "Daha fazla göster", per-section errors with retry, the single refresh timer and the
// brief submission sequence the panel depends on. The page is mounted through the router
// (`@/pages/BriefPage.vue`); requests go through the `ApiClientKey` client; time is
// `vi.useFakeTimers()` + `vi.setSystemTime()`.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { t } from '@/i18n/index'
import { formatTsiPlain, remainingSeconds } from '@/lib/schedule'
import { CAMPAIGN_ID, P, campaign, sel } from '../helpers/p4'
import { fakeEventSourceFactory } from '../helpers/p5'
import {
  SCHEDULED_QUERY,
  SENT_QUERY,
  SUMMARIES_PATH,
  briefRoutes,
  deferred,
  deliveries,
  fail,
  mountBrief,
  overview,
  page,
  panelApi,
  seq,
  sid,
  summary,
  type Answer,
  type PanelApi,
  type Summary,
} from '../helpers/p10'

const NOW = '2026-01-15T12:00:00Z'
const EXAMPLE =
  "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

let wrapper: VueWrapper | undefined

beforeEach(() => {
  // setImmediate and microtasks stay real so flushPromises() keeps working.
  vi.useFakeTimers({
    toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'],
  })
  vi.setSystemTime(new Date(NOW))
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
  vi.useRealTimers()
})

const q = (id: string) => document.querySelector(sel(id)) as HTMLElement | null
const fmt = (n: number) => new Intl.NumberFormat('tr-TR').format(n)
const inSeconds = (s: number) => new Date(Date.parse(NOW) + s * 1000).toISOString()

function scheduledRow(
  n: number,
  aheadSeconds: number,
  extra: Record<string, unknown> = {},
): Summary {
  return summary(n, 'scheduled', { scheduled_at: inSeconds(aheadSeconds), ...extra })
}

function rowsOf(section: string): HTMLElement[] {
  return [...(q(section)?.querySelectorAll(sel('campaign-summary-item')) ?? [])] as HTMLElement[]
}

const rowIds = (section: string) => rowsOf(section).map((a) => a.getAttribute('data-campaign-id'))

async function start(h: Parameters<typeof panelApi>[0], opts: { eventSource?: unknown } = {}) {
  const api = panelApi({ ...h, routes: briefRoutes(h.routes ?? {}) })
  const r = await mountBrief(api.client, { eventSource: opts.eventSource })
  wrapper = r.wrapper
  return { api, router: r.router, w: r.wrapper }
}

const newSummaryCalls = (api: PanelApi, before: number) => api.summaryQueries().slice(before)

async function advance(ms: number) {
  await vi.advanceTimersByTimeAsync(ms)
  await flushPromises()
}

async function typeBrief(w: VueWrapper, text = EXAMPLE) {
  await w.find(sel('brief-input')).setValue(text)
}

// ---------------------------------------------------------------------------------------------

describe('CampaignPanel placement (REQ-DSH-02 C4)', () => {
  // REQ-DSH-02 C4
  it('on brief-new the panel is a section with the h2 title, after brief-submit and the seed note', async () => {
    await start({ scheduled: () => page([]), sent: () => page([]) })
    const panel = q('brief-campaign-panel')
    expect(panel?.tagName).toBe('SECTION')
    const h2 = [...(panel?.querySelectorAll('h2') ?? [])].map((h) => h.textContent?.trim())
    expect(h2).toContain(t('briefPanel.title'))
    const seed = q('brief-seed-note')
    expect(seed?.textContent?.trim()).toBe(t('app.seedNote'))
    const submit = q('brief-submit')
    expect(submit).not.toBeNull()
    expect(submit!.compareDocumentPosition(panel!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(seed!.compareDocumentPosition(panel!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  // REQ-DSH-02 C4
  it('on the read-only brief route there is no panel and no summaries request', async () => {
    const api = panelApi({ scheduled: () => page([]), sent: () => page([]), routes: briefRoutes() })
    const r = await mountBrief(api.client, { route: 'brief' })
    wrapper = r.wrapper
    await flushPromises()
    expect(r.router.currentRoute.value.name).toBe('brief')
    expect(q('brief-campaign-panel')).toBeNull()
    expect(api.summaryCalls()).toHaveLength(0)
  })
})

describe('Brief submission with the panel (REQ-DSH-02 C3, C4)', () => {
  // REQ-DSH-02 C3, C4
  it('POST /api/campaigns then POST segment; the panel is gone while either is pending; success goes to audience', async () => {
    const create = deferred()
    const segment = deferred()
    const { api, router, w } = await start({
      scheduled: () => page([]),
      sent: () => page([]),
      routes: {
        'POST /campaigns': () => create.promise,
        [`POST /campaigns/${CAMPAIGN_ID}/segment`]: () => segment.promise,
      },
    })
    expect(q('brief-campaign-panel')).not.toBeNull()
    await typeBrief(w)
    expect(q('brief-submit')?.getAttribute('aria-disabled')).not.toBe('true')
    await w.find(sel('brief-submit')).trigger('click')
    await flushPromises()

    const posts = api.calls.filter((c) => c.method.toUpperCase() === 'POST')
    expect(posts).toHaveLength(1)
    expect(posts[0]?.path.endsWith('/campaigns')).toBe(true)
    expect(posts[0]?.opts?.body).toEqual({ brief: EXAMPLE })
    expect(q('brief-campaign-panel')).toBeNull()

    create.resolve(campaign('draft', null))
    await flushPromises()
    expect(api.count('POST', `/campaigns/${CAMPAIGN_ID}/segment`)).toBe(1)
    expect(q('brief-campaign-panel')).toBeNull()

    segment.resolve({ campaign_id: CAMPAIGN_ID, state: 'audience_ready' })
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('audience'))
  })

  // REQ-DSH-02 C3
  it('a failed segment request goes to the brief route', async () => {
    const { router, w } = await start({
      scheduled: () => page([]),
      sent: () => page([]),
      routes: { [`POST /campaigns/${CAMPAIGN_ID}/segment`]: fail(502, 'llm_unavailable') },
    })
    await typeBrief(w)
    await w.find(sel('brief-submit')).trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('brief'))
  })

  // REQ-DSH-02 C4
  it('POST /api/campaigns 500 keeps brief-new, remounts the panel and repeats the two initial requests', async () => {
    const { api, router, w } = await start({
      scheduled: () => page([]),
      sent: () => page([]),
      routes: { 'POST /campaigns': fail(500, 'internal_error') },
    })
    expect(api.summaryCalls()).toHaveLength(2)
    await typeBrief(w)
    await w.find(sel('brief-submit')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('brief-new')
    expect(q('brief-campaign-panel')).not.toBeNull()
    const again = newSummaryCalls(api, 2)
    expect(again).toHaveLength(2)
    expect(again).toEqual(expect.arrayContaining([SCHEDULED_QUERY, SENT_QUERY]))
  })
})

describe('CampaignPanel initial requests (REQ-DSH-02 C2, C5, C12)', () => {
  // REQ-DSH-02 C2, C5
  it('mount makes exactly two GET /api/campaign-summaries requests at once, with the exact queries', async () => {
    const s = deferred()
    const d = deferred()
    const { api } = await start({ scheduled: () => s.promise, sent: () => d.promise })
    const calls = api.summaryCalls()
    expect(calls).toHaveLength(2)
    for (const c of calls) {
      expect(c.method).toBe('GET')
      expect(c.path).toBe(SUMMARIES_PATH)
    }
    expect(calls.map((c) => c.opts?.query)).toEqual(
      expect.arrayContaining([SCHEDULED_QUERY, SENT_QUERY]),
    )
    // While the requests are pending the composer is already there.
    expect(q('brief-input')?.tagName).toBe('TEXTAREA')
    expect(q('brief-submit')).not.toBeNull()
    s.resolve(page([]))
    d.resolve(page([]))
    await flushPromises()
    expect(api.summaryCalls()).toHaveLength(2)
  })

  // REQ-DSH-02 C12
  it('opens no EventSource for the listed campaigns', async () => {
    const es = fakeEventSourceFactory()
    await start(
      { scheduled: () => page([scheduledRow(1, 600)]), sent: () => page([summary(2, 'sending')]) },
      { eventSource: es.factory },
    )
    expect(es.created).toHaveLength(0)
  })
})

describe('CampaignPanel KPI cards (REQ-DSH-02 C6)', () => {
  const OV = overview(
    {
      draft: 1200,
      audience_ready: 3,
      content_ready: 4,
      approved: 5,
      scheduled: 6,
      sending: 7,
      completed: 1008,
    },
    deliveries(0, 0, 25061, 1043),
  )
  const KPIS = [
    {
      id: 'campaign-kpi-total',
      label: 'briefPanel.kpi.total' as const,
      value: OV.campaigns.total!,
    },
    { id: 'campaign-kpi-scheduled', label: 'briefPanel.kpi.scheduled' as const, value: 6 },
    { id: 'campaign-kpi-sent', label: 'briefPanel.kpi.sent' as const, value: 7 + 1008 },
    {
      id: 'campaign-kpi-messages-sent',
      label: 'briefPanel.kpi.messagesSent' as const,
      value: 25061,
    },
    {
      id: 'campaign-kpi-messages-failed',
      label: 'briefPanel.kpi.messagesFailed' as const,
      value: 1043,
    },
  ]

  // REQ-DSH-02 C6
  it.each(KPIS)(
    '$id: raw data-value from the overview, tr-TR text, label, no %',
    async ({ id, label, value }) => {
      await start({ scheduled: () => page([], 0, OV), sent: () => page([], 0, OV) })
      const card = q(id)
      expect(card?.closest('dl')).not.toBeNull()
      expect(card?.getAttribute('data-value')).toBe(String(value))
      const dlText = card?.closest('dl')?.textContent ?? ''
      expect(dlText).toContain(t(label))
      expect(card?.textContent).toContain(fmt(value))
      expect(dlText).not.toContain('%')
    },
  )

  // REQ-DSH-02 C6, C11
  it.each([
    {
      name: 'before any response',
      scheduled: () => deferred().promise,
      sent: () => deferred().promise,
    },
    { name: 'when both requests fail', scheduled: fail(), sent: fail() },
  ])('$name every card has no data-value and shows —', async ({ scheduled, sent }) => {
    await start({ scheduled, sent })
    for (const { id } of KPIS) {
      expect(q(id)?.hasAttribute('data-value'), id).toBe(false)
      expect(q(id)?.textContent, id).toContain('—')
    }
  })

  // REQ-DSH-02 C6, C11
  it.each([
    { name: 'scheduled fails', scheduled: fail(), sent: () => page([], 0, OV) },
    { name: 'sent fails', scheduled: () => page([], 0, OV), sent: fail() },
  ])('$name: the cards come from the successful response', async ({ scheduled, sent }) => {
    await start({ scheduled, sent })
    expect(q('campaign-kpi-total')?.getAttribute('data-value')).toBe(String(OV.campaigns.total))
    expect(q('campaign-kpi-messages-failed')?.getAttribute('data-value')).toBe('1043')
  })
})

describe('CampaignPanel sections (REQ-DSH-02 C7, C9)', () => {
  const SECTIONS = [
    {
      name: 'Planlanan',
      root: 'brief-scheduled-summary',
      total: 'brief-scheduled-total',
      list: 'brief-scheduled-list',
      empty: 'brief-scheduled-empty',
      more: 'brief-scheduled-more',
      title: 'briefPanel.scheduledTitle' as const,
      emptyText: 'briefPanel.scheduledEmpty' as const,
      key: 'scheduled' as const,
      rows: [scheduledRow(1, 7200), scheduledRow(2, 9000), scheduledRow(3, 86400)],
    },
    {
      name: 'Gönderilen',
      root: 'brief-sent-summary',
      total: 'brief-sent-total',
      list: 'brief-sent-list',
      empty: 'brief-sent-empty',
      more: 'brief-sent-more',
      title: 'briefPanel.sentTitle' as const,
      emptyText: 'briefPanel.sentEmpty' as const,
      key: 'sent' as const,
      rows: [summary(4, 'completed'), summary(5, 'completed'), summary(6, 'completed')],
    },
  ]

  function answers(key: 'scheduled' | 'sent', mine: Answer) {
    return key === 'scheduled'
      ? { scheduled: mine, sent: () => page([]) }
      : { scheduled: () => page([]), sent: mine }
  }

  // REQ-DSH-02 C7
  it.each(SECTIONS)('$name: loading while the first request is pending, then ready', async (s) => {
    const d = deferred()
    await start(answers(s.key, () => d.promise))
    expect(q(s.root)?.tagName).toBe('SECTION')
    expect(q(s.root)?.getAttribute('data-state')).toBe('loading')
    d.resolve(page(s.rows, 3))
    await flushPromises()
    expect(q(s.root)?.getAttribute('data-state')).toBe('ready')
  })

  // REQ-DSH-02 C7
  it.each(SECTIONS)(
    '$name: h3 title with the total inside it; rows in the list in items order',
    async (s) => {
      await start(answers(s.key, () => page(s.rows, 9)))
      const h3 = q(s.root)?.querySelector('h3')
      expect(h3?.textContent).toContain(t(s.title))
      const total = h3?.querySelector(sel(s.total))
      expect(total?.getAttribute('data-value')).toBe('9')
      const links = rowsOf(s.root)
      expect(links.map((a) => a.getAttribute('data-campaign-id'))).toEqual(s.rows.map((r) => r.id))
      for (const [i, a] of links.entries()) {
        expect(a.tagName).toBe('A')
        expect(a.getAttribute('href')).toBe(`/campaigns/${s.rows[i]!.id}/tracking`)
        expect(a.getAttribute('data-state')).toBe(s.rows[i]!.state)
        const li = a.closest('li')
        expect(li?.parentElement?.tagName).toBe('UL')
        expect(li?.parentElement?.getAttribute('data-testid')).toBe(s.list)
      }
    },
  )

  // REQ-DSH-02 C7
  it('at most 5 rows on the first load', async () => {
    const rows = [1, 2, 3, 4, 5].map((n) => summary(n, 'completed'))
    await start({ scheduled: () => page([]), sent: () => page(rows, 12) })
    expect(rowsOf('brief-sent-summary')).toHaveLength(5)
  })

  // REQ-DSH-02 C7
  it('clicking a row goes to the tracking route of that campaign', async () => {
    const { router } = await start({
      scheduled: () => page([]),
      sent: () => page([summary(4, 'completed')]),
    })
    rowsOf('brief-sent-summary')[0]!.click()
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('tracking'))
    expect(router.currentRoute.value.params.campaignId).toBe(sid(4))
  })

  // REQ-DSH-02 C7, C9
  it.each(SECTIONS)(
    '$name: total 0 is empty with the empty text, no list and no more button',
    async (s) => {
      await start(answers(s.key, () => page([], 0)))
      expect(q(s.root)?.getAttribute('data-state')).toBe('empty')
      expect(q(s.list)).toBeNull()
      expect(q(s.empty)?.textContent?.trim()).toBe(t(s.emptyText))
      expect(q(s.more)).toBeNull()
    },
  )

  // REQ-DSH-02 C7, C10
  it.each(SECTIONS)('$name: no more button when every row is shown', async (s) => {
    await start(answers(s.key, () => page(s.rows, 3)))
    expect(q(s.more)).toBeNull()
  })
})

describe('CampaignPanel rows (REQ-DSH-02 C8, C2, C14)', () => {
  // REQ-DSH-02 C8
  it('campaign-summary-brief is the first line of the brief', async () => {
    const row = summary(1, 'completed', {
      brief: 'Birinci satır: İzmir kampanyası\nİkinci satır gizli',
    })
    await start({ scheduled: () => page([]), sent: () => page([row]) })
    const b = q('brief-sent-summary')?.querySelector(sel('campaign-summary-brief'))
    expect(b?.textContent).toBe('Birinci satır: İzmir kampanyası')
    expect(rowsOf('brief-sent-summary')[0]?.contains(b!)).toBe(true)
  })

  // REQ-DSH-02 C8
  it('a scheduled row: time in TSİ without the zone suffix and remaining seconds at load, not ticking', async () => {
    const row = scheduledRow(1, 7200)
    await start({ scheduled: () => page([row]), sent: () => page([]) })
    const link = rowsOf('brief-scheduled-summary')[0]!
    const time = link.querySelector(sel('campaign-summary-time'))
    expect(time?.getAttribute('data-value')).toBe(row.scheduled_at)
    expect(time?.textContent?.trim()).toBe(formatTsiPlain(row.scheduled_at!))
    const rel = link.querySelector(sel('campaign-summary-relative'))
    expect(rel?.getAttribute('data-value')).toBe(
      String(remainingSeconds(row.scheduled_at!, Date.now())),
    )
    expect(rel?.getAttribute('data-value')).toBe('7200')
    await advance(5000)
    expect(link.querySelector(sel('campaign-summary-relative'))?.getAttribute('data-value')).toBe(
      '7200',
    )
  })

  // REQ-DSH-02 C8
  it('a sent row: state badge, progress bar, sent / failed and the start time', async () => {
    const row = summary(1, 'sending', {
      deliveries: deliveries(100, 20, 300, 7),
      sending_started_at: '2026-01-15T11:58:30Z',
    })
    await start({ scheduled: () => page([]), sent: () => page([row]) })
    const link = rowsOf('brief-sent-summary')[0]!
    expect(link.querySelector(sel('campaign-summary-state'))?.getAttribute('data-status')).toBe(
      'sending',
    )
    const bar = link.querySelector(sel('campaign-summary-bar'))
    expect(bar?.getAttribute('role')).toBe('progressbar')
    expect(bar?.getAttribute('aria-valuenow')).toBe('307')
    expect(bar?.getAttribute('aria-valuemax')).toBe('427')
    expect(link.querySelector(sel('campaign-summary-sent'))?.getAttribute('data-value')).toBe('300')
    expect(link.querySelector(sel('campaign-summary-failed'))?.getAttribute('data-value')).toBe('7')
    const time = link.querySelector(sel('campaign-summary-time'))
    expect(time?.getAttribute('data-value')).toBe('2026-01-15T11:58:30Z')
    expect(time?.textContent?.trim()).toBe(formatTsiPlain('2026-01-15T11:58:30Z'))
  })

  // REQ-DSH-02 C2
  it.each([
    {
      name: 'a scheduled row',
      scheduled: [scheduledRow(1, 7200)],
      sent: [] as Summary[],
      shown: true,
    },
    {
      name: 'a sent row',
      scheduled: [] as Summary[],
      sent: [summary(2, 'completed')],
      shown: true,
    },
    { name: 'no row at all', scheduled: [] as Summary[], sent: [] as Summary[], shown: false },
  ])('brief-panel-tz-note with $name', async ({ scheduled, sent, shown }) => {
    await start({ scheduled: () => page(scheduled), sent: () => page(sent) })
    const notes = document.querySelectorAll(sel('brief-panel-tz-note'))
    expect(notes).toHaveLength(shown ? 1 : 0)
    if (shown) expect(notes[0]?.textContent?.trim()).toBe(t('schedule.tzNote'))
  })

  // REQ-DSH-02 C14
  it('the brief is printed as text: no script or b element under the row', async () => {
    await start({
      scheduled: () => page([]),
      sent: () => page([summary(1, 'completed', { brief: P })]),
    })
    const link = rowsOf('brief-sent-summary')[0]!
    expect(link.querySelector(sel('campaign-summary-brief'))?.textContent).toBe(P)
    const li = link.closest('li')!
    expect(li.querySelector('script')).toBeNull()
    expect(li.querySelector('b')).toBeNull()
  })
})

describe('CampaignPanel "Daha fazla göster" (REQ-DSH-02 C10)', () => {
  const FIRST = [1, 2, 3, 4, 5].map((n) => summary(n, 'completed'))

  // REQ-DSH-02 C10
  it('is shown when fewer rows than total; asks offset 5 with the same state/order; data-loading while pending', async () => {
    const more = deferred()
    const { api } = await start({
      scheduled: () => page([]),
      sent: seq(
        () => page(FIRST, 7),
        () => more.promise,
      ),
    })
    const btn = q('brief-sent-more')
    expect(btn?.textContent?.trim()).toBe(t('briefPanel.more'))
    btn!.click()
    await flushPromises()
    expect(api.summaryQueries().at(-1)).toEqual({
      state: 'sending,completed',
      order: 'recent',
      limit: 5,
      offset: 5,
    })
    expect(q('brief-sent-more')?.getAttribute('data-loading')).toBe('true')
    more.resolve(page([summary(6, 'completed'), summary(7, 'completed')], 7, overview(), 5))
    await flushPromises()
  })

  // REQ-DSH-02 C10
  it.each([
    {
      name: 'two new rows: appended, button gone, focus on the first new row, announced once',
      reply: [summary(6, 'completed'), summary(7, 'completed')],
      wantIds: [1, 2, 3, 4, 5, 6, 7],
      button: false,
      focus: 6 as number | null,
      n: 2 as number | null,
    },
    {
      name: 'a duplicate is skipped: one new row, button stays',
      reply: [summary(5, 'completed'), summary(6, 'completed')],
      wantIds: [1, 2, 3, 4, 5, 6],
      button: true,
      focus: 6 as number | null,
      n: 1 as number | null,
    },
    {
      name: 'only duplicates: nothing added, focus stays on the button, no announcement',
      reply: [summary(4, 'completed'), summary(5, 'completed')],
      wantIds: [1, 2, 3, 4, 5],
      button: true,
      focus: null as number | null,
      n: null as number | null,
    },
  ])('$name', async ({ reply, wantIds, button, focus, n }) => {
    await start({
      scheduled: () => page([]),
      sent: seq(
        () => page(FIRST, 7),
        () => page(reply, 7, overview(), 5),
      ),
    })
    const btn = q('brief-sent-more')!
    btn.focus()
    btn.click()
    await flushPromises()
    expect(rowIds('brief-sent-summary')).toEqual(wantIds.map(sid))
    expect(q('brief-sent-more') !== null).toBe(button)
    const status = q('brief-panel-status')
    expect(status?.getAttribute('role')).toBe('status')
    if (focus === null) {
      expect(document.activeElement).toBe(q('brief-sent-more'))
      expect(status?.textContent?.trim() ?? '').toBe('')
    } else {
      const target = rowsOf('brief-sent-summary').find(
        (a) => a.getAttribute('data-campaign-id') === sid(focus),
      )
      expect(document.activeElement).toBe(target)
      expect(status?.textContent?.trim()).toBe(t('briefPanel.moreLoaded', { n: n! }))
    }
  })

  // REQ-DSH-02 C10
  it('the next click asks offset = rows shown', async () => {
    const { api } = await start({
      scheduled: () => page([]),
      sent: seq(
        () => page(FIRST, 9),
        () => page([summary(5, 'completed'), summary(6, 'completed')], 9, overview(), 5),
        () => page([summary(7, 'completed')], 9, overview(), 6),
      ),
    })
    q('brief-sent-more')!.click()
    await flushPromises()
    q('brief-sent-more')!.click()
    await flushPromises()
    expect(api.summaryQueries().at(-1)).toEqual({
      state: 'sending,completed',
      order: 'recent',
      limit: 5,
      offset: 6,
    })
    expect(rowIds('brief-sent-summary')).toEqual([1, 2, 3, 4, 5, 6, 7].map(sid))
  })

  // REQ-DSH-02 C10
  it('works the same for the Planlanan section', async () => {
    const first = [1, 2, 3, 4, 5].map((i) => scheduledRow(i, 7200 + i))
    const { api } = await start({
      scheduled: seq(
        () => page(first, 6),
        () => page([scheduledRow(6, 9000)], 6, overview(), 5),
      ),
      sent: () => page([]),
    })
    q('brief-scheduled-more')!.click()
    await flushPromises()
    expect(api.summaryQueries().at(-1)).toEqual({
      state: 'scheduled',
      order: 'scheduled_at',
      limit: 5,
      offset: 5,
    })
    expect(rowIds('brief-scheduled-summary')).toEqual([1, 2, 3, 4, 5, 6].map(sid))
    expect(q('brief-scheduled-more')).toBeNull()
  })
})

describe('CampaignPanel error isolation (REQ-DSH-02 C11)', () => {
  const SIDES = [
    {
      name: 'Planlanan fails',
      failing: 'scheduled' as const,
      root: 'brief-scheduled-summary',
      error: 'brief-scheduled-error',
      retry: 'brief-scheduled-retry',
      errorText: 'briefPanel.scheduledError' as const,
      other: 'brief-sent-summary',
      query: SCHEDULED_QUERY,
      okRows: [scheduledRow(1, 7200)],
    },
    {
      name: 'Gönderilen fails',
      failing: 'sent' as const,
      root: 'brief-sent-summary',
      error: 'brief-sent-error',
      retry: 'brief-sent-retry',
      errorText: 'briefPanel.sentError' as const,
      other: 'brief-scheduled-summary',
      query: SENT_QUERY,
      okRows: [summary(2, 'completed')],
    },
  ]

  function answersFor(s: (typeof SIDES)[number], failing: Answer) {
    const other = s.failing === 'scheduled' ? [summary(9, 'completed')] : [scheduledRow(9, 7200)]
    return s.failing === 'scheduled'
      ? { scheduled: failing, sent: () => page(other) }
      : { scheduled: () => page(other), sent: failing }
  }

  // REQ-DSH-02 C11, C7
  it.each(SIDES)(
    '$name: only that section shows the error; the other section, the composer and submission work',
    async (s) => {
      const { api, w } = await start(answersFor(s, fail(500, 'internal_error')))
      expect(q(s.root)?.getAttribute('data-state')).toBe('error')
      expect(q(s.error)?.getAttribute('data-code')).toBe('internal_error')
      expect(q(s.error)?.textContent?.trim()).toBe(t(s.errorText))
      expect(q(s.retry)?.textContent?.trim()).toBe(t('briefPanel.retry'))
      expect(q(s.other)?.getAttribute('data-state')).toBe('ready')
      expect(rowsOf(s.other)).toHaveLength(1)
      await typeBrief(w)
      expect(q('brief-submit')?.getAttribute('aria-disabled')).not.toBe('true')
      await w.find(sel('brief-submit')).trigger('click')
      await flushPromises()
      expect(api.count('POST', '/campaigns')).toBe(1)
    },
  )

  // REQ-DSH-02 C11
  it.each(SIDES)(
    '$name: retry repeats the same query and success removes the error elements',
    async (s) => {
      const okPage = () => page(s.okRows)
      const { api } = await start(answersFor(s, seq(fail(500, 'internal_error'), okPage)))
      const before = api.summaryCalls().length
      q(s.retry)!.click()
      await flushPromises()
      expect(newSummaryCalls(api, before)).toEqual([s.query])
      expect(q(s.error)).toBeNull()
      expect(q(s.retry)).toBeNull()
      expect(q(s.root)?.getAttribute('data-state')).toBe('ready')
    },
  )

  // REQ-DSH-02 C11
  it('a failed "more" keeps the rows, shows the error and retry repeats the same more request', async () => {
    const first = [1, 2, 3, 4, 5].map((n) => summary(n, 'completed'))
    const { api } = await start({
      scheduled: () => page([]),
      sent: seq(
        () => page(first, 7),
        fail(500, 'internal_error'),
        () => page([summary(6, 'completed'), summary(7, 'completed')], 7, overview(), 5),
      ),
    })
    q('brief-sent-more')!.click()
    await flushPromises()
    expect(rowIds('brief-sent-summary')).toEqual([1, 2, 3, 4, 5].map(sid))
    expect(q('brief-sent-error')?.getAttribute('data-code')).toBe('internal_error')
    const before = api.summaryCalls().length
    q('brief-sent-retry')!.click()
    await flushPromises()
    expect(newSummaryCalls(api, before)).toEqual([
      { state: 'sending,completed', order: 'recent', limit: 5, offset: 5 },
    ])
    expect(rowIds('brief-sent-summary')).toEqual([1, 2, 3, 4, 5, 6, 7].map(sid))
    expect(q('brief-sent-error')).toBeNull()
  })
})

describe('CampaignPanel refresh timer (REQ-DSH-02 C12)', () => {
  const REFRESH = [SCHEDULED_QUERY, SENT_QUERY]

  // REQ-DSH-02 C12 (test a)
  it('(a) a sending row: nothing at 9999 ms, exactly the two refresh requests at 10 000 ms', async () => {
    const { api } = await start({
      scheduled: () => page([]),
      sent: () => page([summary(1, 'sending')]),
    })
    expect(api.summaryCalls()).toHaveLength(2)
    await advance(9999)
    expect(api.summaryCalls()).toHaveLength(2)
    await advance(1)
    const fresh = newSummaryCalls(api, 2)
    expect(fresh).toHaveLength(2)
    expect(fresh).toEqual(expect.arrayContaining(REFRESH))
  })

  // REQ-DSH-02 C12 (delayed start)
  it('a retry that brings a sending row starts the timer: exactly two requests after 10 000 ms', async () => {
    const { api } = await start({
      scheduled: () => page([]),
      sent: seq(fail(500, 'internal_error'), () => page([summary(1, 'sending')])),
    })
    q('brief-sent-retry')!.click()
    await flushPromises()
    const before = api.summaryCalls().length
    await advance(9999)
    expect(api.summaryCalls()).toHaveLength(before)
    await advance(1)
    expect(newSummaryCalls(api, before)).toHaveLength(2)
  })

  // REQ-DSH-02 C12 (test b)
  it.each([
    { name: 'r = 120 s fires at r*1000 + 10 000 ms', ahead: 120, fireAt: 120 * 1000 + 10000 },
    { name: 'r = 3600 s (the 1 h edge) still fires', ahead: 3600, fireAt: 3600 * 1000 + 10000 },
  ])('(b) earliest plan $name', async ({ ahead, fireAt }) => {
    const { api } = await start({
      scheduled: () => page([scheduledRow(1, ahead), scheduledRow(2, ahead + 600)]),
      sent: () => page([summary(3, 'completed')]),
    })
    await advance(fireAt - 1)
    expect(api.summaryCalls()).toHaveLength(2)
    await advance(1)
    const fresh = newSummaryCalls(api, 2)
    expect(fresh).toHaveLength(2)
    expect(fresh).toEqual(expect.arrayContaining(REFRESH))
  })

  // REQ-DSH-02 C12 (test b, r > 3600)
  it('(b) a plan 3601 s ahead sets no timer: nothing after 3 700 000 ms', async () => {
    const { api } = await start({
      scheduled: () => page([scheduledRow(1, 3601)]),
      sent: () => page([]),
    })
    await advance(3_700_000)
    expect(api.summaryCalls()).toHaveLength(2)
  })

  // REQ-DSH-02 C12
  it('the smaller candidate wins: a sending row and a plan 120 s ahead refresh at 10 000 ms', async () => {
    const { api } = await start({
      scheduled: () => page([scheduledRow(1, 120)]),
      sent: () => page([summary(2, 'sending')]),
    })
    await advance(10000)
    expect(newSummaryCalls(api, 2)).toHaveLength(2)
  })

  // REQ-DSH-02 C12 (quiet)
  it('quiet: no sending row and no plan within 1 h: nothing after 60 000 ms; nothing after unmount', async () => {
    const { api, w } = await start({
      scheduled: () => page([scheduledRow(1, 7200)]),
      sent: () => page([summary(2, 'completed')]),
    })
    await advance(60000)
    expect(api.summaryCalls()).toHaveLength(2)
    w.unmount()
    wrapper = undefined
    await advance(60000)
    expect(api.summaryCalls()).toHaveLength(2)
  })

  // REQ-DSH-02 C12
  it.each(['unmount', 'route change'] as const)(
    '%s clears a pending timer: no request afterwards',
    async (how) => {
      const { api, w, router } = await start({
        scheduled: () => page([]),
        sent: () => page([summary(1, 'sending')]),
      })
      if (how === 'unmount') {
        w.unmount()
        wrapper = undefined
      } else {
        await router.push(`/campaigns/${CAMPAIGN_ID}/audience`)
      }
      await flushPromises()
      expect(q('brief-campaign-panel')).toBeNull()
      await advance(60000)
      expect(api.summaryCalls()).toHaveLength(2)
    },
  )

  // REQ-DSH-02 C12
  it('the refresh asks offset 0 and limit = rows shown (at least 5)', async () => {
    const first = [1, 2, 3, 4, 5].map((n) => summary(n, 'sending'))
    const { api } = await start({
      scheduled: () => page([]),
      sent: seq(
        () => page(first, 7),
        () => page([summary(6, 'completed'), summary(7, 'completed')], 7, overview(), 5),
        () => page([...first, summary(6, 'completed'), summary(7, 'completed')], 7),
      ),
    })
    q('brief-sent-more')!.click()
    await flushPromises()
    const before = api.summaryCalls().length
    await advance(10000)
    const fresh = newSummaryCalls(api, before)
    expect(fresh).toHaveLength(2)
    expect(fresh).toEqual(
      expect.arrayContaining([
        { state: 'scheduled', order: 'scheduled_at', limit: 5, offset: 0 },
        { state: 'sending,completed', order: 'recent', limit: 7, offset: 0 },
      ]),
    )
  })

  // REQ-DSH-02 C12, C6
  it('a refresh replaces rows, totals and cards in place without loading; a started plan moves to Gönderilen', async () => {
    const refreshScheduled = deferred()
    const OV2 = overview({ scheduled: 1, sending: 1 })
    const plan = scheduledRow(1, 120)
    await start({
      scheduled: seq(
        () => page([plan], 1),
        () => refreshScheduled.promise,
      ),
      sent: seq(
        () => page([], 0),
        () => page([summary(1, 'sending', { scheduled_at: plan.scheduled_at })], 1, OV2),
      ),
    })
    expect(q('brief-scheduled-summary')?.getAttribute('data-state')).toBe('ready')
    await advance(130_000)
    // One refresh answer is still pending: no loading state on either section.
    expect(q('brief-scheduled-summary')?.getAttribute('data-state')).toBe('ready')
    expect(rowIds('brief-scheduled-summary')).toEqual([sid(1)])
    refreshScheduled.resolve(page([], 0, OV2))
    await flushPromises()
    expect(q('brief-scheduled-summary')?.getAttribute('data-state')).toBe('empty')
    expect(q('brief-scheduled-total')?.getAttribute('data-value')).toBe('0')
    expect(q('brief-sent-total')?.getAttribute('data-value')).toBe('1')
    const sentRow = rowsOf('brief-sent-summary')[0]
    expect(sentRow?.getAttribute('data-campaign-id')).toBe(sid(1))
    expect(sentRow?.getAttribute('data-state')).toBe('sending')
    expect(q('campaign-kpi-scheduled')?.getAttribute('data-value')).toBe('1')
    expect(q('campaign-kpi-sent')?.getAttribute('data-value')).toBe(
      String(OV2.campaigns.sending! + OV2.campaigns.completed!),
    )
  })

  // REQ-DSH-02 C12
  it('a failed refresh shows no error, keeps the rows and stops the timer', async () => {
    const { api } = await start({
      scheduled: seq(() => page([]), fail()),
      sent: seq(() => page([summary(1, 'sending')]), fail()),
    })
    await advance(10000)
    expect(newSummaryCalls(api, 2)).toHaveLength(2)
    expect(rowIds('brief-sent-summary')).toEqual([sid(1)])
    expect(q('brief-sent-error')).toBeNull()
    expect(q('brief-scheduled-error')).toBeNull()
    await advance(60000)
    expect(api.summaryCalls()).toHaveLength(4)
  })

  // REQ-DSH-02 C12
  it('no refresh while a "more" request is pending; its response sets the timer again', async () => {
    const more = deferred()
    const first = [1, 2, 3, 4, 5].map((n) => summary(n, 'sending'))
    const { api } = await start({
      scheduled: () => page([]),
      sent: seq(
        () => page(first, 7),
        () => more.promise,
        () => page(first, 7),
      ),
    })
    q('brief-sent-more')!.click()
    await flushPromises()
    expect(api.summaryCalls()).toHaveLength(3)
    await advance(10000)
    expect(api.summaryCalls()).toHaveLength(3)
    more.resolve(page([summary(6, 'sending'), summary(7, 'sending')], 7, overview(), 5))
    await flushPromises()
    await advance(9999)
    expect(api.summaryCalls()).toHaveLength(3)
    await advance(1)
    expect(newSummaryCalls(api, 3)).toHaveLength(2)
  })
})
