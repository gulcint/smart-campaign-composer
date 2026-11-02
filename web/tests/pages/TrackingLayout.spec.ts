// REQ-DSH-03 (UI): the tracking page information layout of P10: the campaign header block (brief,
// audience, start/end meta row with one time-zone note), the overall status card (percent,
// processed sentence, results / remaining groups, waiting state), liveness and last-updated, channel
// cards, heading order, the scheduled view and the phone variant cards next to the P9 table.
// Setup as REQ-TRK-01: `@/pages/TrackingPage.vue` on route `tracking`, a fake EventSource factory
// (`EventSourceFactoryKey`), the client (`ApiClientKey`) and `vi.useFakeTimers()`. The REQ-TRK-01,
// REQ-SCH-04 and REQ-ABT-04 assertions live in TrackingPage.spec.ts and are not repeated here.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import TrackingPage from '@/pages/TrackingPage.vue'
import type { Campaign } from '@/api/types'
import { t } from '@/i18n/index'
import { formatTsiPlain } from '@/lib/schedule'
import { mountPage } from '../helpers/pages'
import { CAMPAIGN_ID, P, T0, V1, V3, campaign, fakeApi, sel, version } from '../helpers/p4'
import { CHANNELS, fakeEventSourceFactory, status, type Counts } from '../helpers/p5'

const C = `/campaigns/${CAMPAIGN_ID}`
const NOW = '2026-01-15T12:05:00Z'
const ZERO: Counts = [0, 0, 0, 0]

let wrapper: VueWrapper | undefined

beforeEach(() => {
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
const counters = () => q('delivery-counters')

async function mountTracking(camp: Campaign = campaign('sending', version())) {
  const es = fakeEventSourceFactory()
  const a = fakeApi({
    [`GET ${C}`]: () => camp,
    [`GET ${C}/deliveries`]: () => ({ items: [], total: 0, limit: 50, offset: 0 }),
  })
  const r = await mountPage(TrackingPage, 'tracking', { api: a.client, eventSource: es.factory })
  wrapper = r.wrapper
  return { es, w: r.wrapper }
}

async function mountWithEvents(events: Array<[string, unknown]>, camp?: Campaign) {
  const m = await mountTracking(camp)
  m.es.last().simulateOpen()
  for (const [name, data] of events) m.es.last().simulateEvent(name, data)
  await flushPromises()
  return m
}

/** The <dt> right before a <dd>, if any. */
const dtOf = (dd: HTMLElement | null) => {
  const prev = dd?.previousElementSibling
  return prev?.tagName === 'DT' ? (prev as HTMLElement) : null
}

const dtTexts = () => [...document.querySelectorAll('dt')].map((d) => d.textContent?.trim())

function headingsIn(root: Element | null) {
  return [...(root?.querySelectorAll('h1, h2, h3, h4, h5, h6') ?? [])] as HTMLElement[]
}

// ---------------------------------------------------------------------------------------------

describe('TrackingPage campaign header block (REQ-DSH-03 C3)', () => {
  // REQ-DSH-03 C3
  it('shows the full brief and the channel audiences of the segment', async () => {
    const camp = campaign('sending', version(), {
      brief: 'Birinci satır uzun bir brief\nİkinci satır ğüşİı',
    })
    await mountTracking(camp)
    expect(q('tracking-campaign-brief')?.textContent).toBe(
      'Birinci satır uzun bir brief\nİkinci satır ğüşİı',
    )
    expect(q('tracking-audience-sms')?.getAttribute('data-value')).toBe('350')
    expect(q('tracking-audience-email')?.getAttribute('data-value')).toBe('368')
  })

  // REQ-DSH-03 C3
  it('prints the brief as text: a <script> brief creates no script element', async () => {
    await mountTracking(campaign('sending', version(), { brief: P }))
    const b = q('tracking-campaign-brief')
    expect(b?.textContent).toBe(P)
    expect(b?.querySelector('script')).toBeNull()
    expect(b?.querySelector('b')).toBeNull()
  })

  // REQ-DSH-03 C3
  it('without a segment there are no audience elements', async () => {
    await mountTracking(campaign('sending', version(), { segment: null } as Partial<Campaign>))
    expect(q('tracking-audience-sms')).toBeNull()
    expect(q('tracking-audience-email')).toBeNull()
  })

  // REQ-DSH-03 C3
  it('tracking-started-at: a <dd> after its <dt>, from the campaign before any event, then from the last event', async () => {
    const m = await mountTracking(campaign('sending', version()))
    let dd = q('tracking-started-at')
    expect(dd?.tagName).toBe('DD')
    expect(dd?.closest('dl')).not.toBeNull()
    expect(dtOf(dd)?.textContent?.trim()).toBe(t('tracking.startedAt'))
    expect(dd?.getAttribute('data-value')).toBe(T0)
    expect(dd?.textContent?.trim()).toBe(formatTsiPlain(T0))

    const later = '2026-01-15T12:00:03Z'
    m.es.last().simulateOpen()
    m.es
      .last()
      .simulateEvent(
        'snapshot',
        status(CAMPAIGN_ID, 'sending', [5, 1, 2, 0], ZERO, { sending_started_at: later }),
      )
    await flushPromises()
    dd = q('tracking-started-at')
    expect(dd?.getAttribute('data-value')).toBe(later)
    expect(dd?.textContent?.trim()).toBe(formatTsiPlain(later))
  })

  // REQ-DSH-03 C3
  it('one tracking-tz-note in the header block while a time is shown', async () => {
    await mountTracking(campaign('sending', version()))
    const notes = document.querySelectorAll(sel('tracking-tz-note'))
    expect(notes).toHaveLength(1)
    expect(notes[0]?.textContent?.trim()).toBe(t('schedule.tzNote'))
  })

  // REQ-DSH-03 C3
  it('tracking-completed-at follows the same rule from completed_at', async () => {
    const m = await mountTracking(campaign('sending', version()))
    expect(q('tracking-completed-at')).toBeNull()
    expect(dtTexts()).not.toContain(t('tracking.completedAt'))

    const done = status(CAMPAIGN_ID, 'completed', [0, 0, 7, 1], [0, 0, 3, 0], {
      completed_at: '2026-01-15T12:04:10Z',
    })
    m.es.last().simulateOpen()
    m.es.last().simulateEvent('snapshot', done)
    m.es.last().simulateEvent('completed', done)
    await flushPromises()
    const dd = q('tracking-completed-at')
    expect(dd?.tagName).toBe('DD')
    expect(dtOf(dd)?.textContent?.trim()).toBe(t('tracking.completedAt'))
    expect(dd?.getAttribute('data-value')).toBe('2026-01-15T12:04:10Z')
    expect(dd?.textContent?.trim()).toBe(formatTsiPlain('2026-01-15T12:04:10Z'))
  })

  // REQ-DSH-03 C3
  it('a null start (planned campaign): neither the <dd> nor its <dt>, and no tz note in the header block', async () => {
    const at = '2026-01-15T13:00:00Z'
    const camp = campaign('scheduled', version(V1, 1), {
      approval: { id: V3, content_version_id: V1, approved_at: T0 },
      scheduled_at: at,
      sending_started_at: null,
      completed_at: null,
    } as Partial<Campaign>)
    await mountWithEvents(
      [
        [
          'snapshot',
          status(CAMPAIGN_ID, 'scheduled', ZERO, ZERO, {
            scheduled_at: at,
            sending_started_at: null,
          }),
        ],
      ],
      camp,
    )
    expect(q('tracking-started-at')).toBeNull()
    expect(dtTexts()).not.toContain(t('tracking.startedAt'))
    expect(q('tracking-tz-note')).toBeNull()
  })

  // REQ-DSH-03 C3
  it('schedule-started-note is shown in the same meta row as tracking-started-at', async () => {
    const at = '2026-01-15T12:01:30Z'
    const camp = campaign('sending', version(V1, 1), {
      approval: { id: V3, content_version_id: V1, approved_at: T0 },
      scheduled_at: at,
      sending_started_at: at,
    } as Partial<Campaign>)
    await mountWithEvents(
      [
        [
          'snapshot',
          status(CAMPAIGN_ID, 'sending', [10, 0, 5, 0], ZERO, {
            scheduled_at: at,
            sending_started_at: at,
          }),
        ],
      ],
      camp,
    )
    const note = q('schedule-started-note')
    const meta = q('tracking-started-at')?.closest('dl')
    expect(note).not.toBeNull()
    expect(meta).toBeTruthy()
    expect(meta?.contains(note)).toBe(true)
  })
})

describe('TrackingPage overall status card (REQ-DSH-03 C4)', () => {
  // REQ-DSH-03 C4
  it.each([
    {
      name: '3 of 7 is 42 (floor)',
      sms: [3, 1, 2, 1] as Counts,
      email: ZERO,
      done: false,
      pct: 42,
    },
    {
      name: '2 of 3 is 66 (floor, not 67)',
      sms: [1, 0, 1, 1] as Counts,
      email: ZERO,
      done: false,
      pct: 66,
    },
    { name: '999 of 1000 is 99', sms: [1, 0, 999, 0] as Counts, email: ZERO, done: false, pct: 99 },
    {
      name: '1500 of 1500 across channels is 100',
      sms: [0, 0, 700, 50] as Counts,
      email: [0, 0, 740, 10] as Counts,
      done: true,
      pct: 100,
    },
    { name: 'total 0 while live is 0', sms: ZERO, email: ZERO, done: false, pct: 0 },
    { name: 'total 0 when completed is 100', sms: ZERO, email: ZERO, done: true, pct: 100 },
  ])('delivery-percent and delivery-processed: $name', async ({ sms, email, done, pct }) => {
    const st = status(CAMPAIGN_ID, done ? 'completed' : 'sending', sms, email)
    await mountWithEvents(
      done
        ? [
            ['snapshot', st],
            ['completed', st],
          ]
        : [['snapshot', st]],
    )
    const percent = q('delivery-percent')
    expect(percent?.getAttribute('data-value')).toBe(String(pct))
    expect(percent?.textContent?.trim()).toBe(`%${pct}`)
    const finished = st.counts.sent + st.counts.failed
    const processed = q('delivery-processed')
    expect(processed?.getAttribute('data-value')).toBe(String(finished))
    expect(processed?.textContent?.trim()).toBe(
      t('tracking.processed', { done: fmt(finished), total: fmt(st.counts.total) }),
    )
    const total = processed?.querySelector(sel('delivery-count-total-total'))
    expect(total?.getAttribute('data-value')).toBe(String(st.counts.total))
  })

  // REQ-DSH-03 C4
  it('the first heading is the h2 overall title; results and remaining group their counters', async () => {
    await mountWithEvents([
      ['snapshot', status(CAMPAIGN_ID, 'sending', [5, 2, 3, 1], [4, 0, 6, 0])],
    ])
    const first = headingsIn(counters())[0]
    expect(first?.tagName).toBe('H2')
    expect(first?.textContent?.trim()).toBe(t('tracking.overallTitle'))
    const results = q('delivery-results')
    expect(results?.querySelector(sel('delivery-count-total-sent'))).not.toBeNull()
    expect(results?.querySelector(sel('delivery-count-total-failed'))).not.toBeNull()
    const remaining = q('delivery-remaining')
    expect(remaining?.querySelector(sel('delivery-count-total-sending'))).not.toBeNull()
    expect(remaining?.querySelector(sel('delivery-count-total-queued'))).not.toBeNull()
  })

  // REQ-DSH-03 C4
  it('before the first status: no data-value and — on percent and processed; delivery-waiting is a status, not a heading', async () => {
    const m = await mountTracking()
    for (const id of ['delivery-percent', 'delivery-processed']) {
      expect(q(id)?.hasAttribute('data-value'), id).toBe(false)
      expect(q(id)?.textContent, id).toContain('—')
    }
    const waiting = q('delivery-waiting')
    expect(waiting?.getAttribute('role')).toBe('status')
    expect(waiting?.textContent?.trim()).toBe(t('delivery.waiting'))
    const asHeading = [...(counters()?.querySelectorAll('h2, h3') ?? [])].filter(
      (h) => h.textContent?.trim() === t('delivery.waiting'),
    )
    expect(asHeading).toHaveLength(0)

    m.es.last().simulateOpen()
    m.es.last().simulateEvent('snapshot', status(CAMPAIGN_ID, 'sending', [5, 0, 0, 0], ZERO))
    await flushPromises()
    expect(q('delivery-waiting')).toBeNull()
  })
})

describe('TrackingPage liveness and last update (REQ-DSH-03 C5)', () => {
  const U1 = '2026-01-15T12:00:05.123456Z'
  const U2 = '2026-01-15T12:00:09.5Z'

  // REQ-DSH-03 C5
  it('live: delivery-live and delivery-last-updated = updated_at of the last event', async () => {
    const m = await mountWithEvents([
      ['snapshot', status(CAMPAIGN_ID, 'sending', [5, 0, 1, 0], ZERO, { updated_at: U1 })],
    ])
    expect(counters()?.getAttribute('data-state')).toBe('live')
    expect(q('delivery-live')?.textContent?.trim()).toBe(t('delivery.live'))
    expect(q('delivery-last-updated')?.getAttribute('data-value')).toBe(U1)
    m.es
      .last()
      .simulateEvent(
        'status',
        status(CAMPAIGN_ID, 'sending', [3, 0, 3, 0], ZERO, { updated_at: U2 }),
      )
    await flushPromises()
    expect(q('delivery-last-updated')?.getAttribute('data-value')).toBe(U2)
  })

  // REQ-DSH-03 C5
  it('stale: no delivery-live; last-updated stays with the stale note', async () => {
    const m = await mountWithEvents([
      ['snapshot', status(CAMPAIGN_ID, 'sending', [5, 0, 1, 0], ZERO, { updated_at: U1 })],
    ])
    m.es.last().simulateError(2)
    await flushPromises()
    expect(counters()?.getAttribute('data-state')).toBe('stale')
    expect(q('delivery-live')).toBeNull()
    expect(q('delivery-last-updated')?.getAttribute('data-value')).toBe(U1)
    expect(q('delivery-stale-note')).not.toBeNull()
  })

  // REQ-DSH-03 C5
  it('completed: no delivery-live; last-updated and the completed title are shown', async () => {
    const done = status(CAMPAIGN_ID, 'completed', [0, 0, 6, 0], ZERO, { updated_at: U2 })
    await mountWithEvents([
      ['snapshot', status(CAMPAIGN_ID, 'sending', [5, 0, 1, 0], ZERO, { updated_at: U1 })],
      ['completed', done],
    ])
    expect(counters()?.getAttribute('data-state')).toBe('completed')
    expect(q('delivery-live')).toBeNull()
    expect(q('delivery-last-updated')?.getAttribute('data-value')).toBe(U2)
    expect(q('delivery-completed-title')?.textContent?.trim()).toBe(t('delivery.completedTitle'))
  })
})

describe('TrackingPage channel cards (REQ-DSH-03 C6)', () => {
  // REQ-DSH-03 C6
  it.each([
    {
      name: 'sending: floor per channel, an empty channel is 0',
      state: 'sending',
      sms: [1, 0, 1, 1] as Counts,
      email: ZERO,
      want: { sms: 66, email: 0 },
    },
    {
      name: 'completed: an empty channel is 100',
      state: 'completed',
      sms: [0, 0, 9, 1] as Counts,
      email: ZERO,
      want: { sms: 100, email: 100 },
    },
    {
      name: 'sending: 1 of 8 is 12',
      state: 'sending',
      sms: [7, 0, 1, 0] as Counts,
      email: [0, 1, 0, 0] as Counts,
      want: { sms: 12, email: 0 },
    },
  ])('$name', async ({ state, sms, email, want }) => {
    const st = status(CAMPAIGN_ID, state, sms, email)
    await mountWithEvents(
      state === 'completed'
        ? [
            ['snapshot', st],
            ['completed', st],
          ]
        : [['snapshot', st]],
    )
    for (const c of CHANNELS) {
      const card = q(`delivery-card-${c}`)
      const h3 = card?.querySelector('h3')
      expect(h3?.textContent?.trim(), c).toBe(t(`tabs.${c}`))
      expect(card?.querySelector(sel(`delivery-percent-${c}`))?.getAttribute('data-value'), c).toBe(
        String(want[c]),
      )
      for (const k of ['queued', 'sending', 'sent', 'failed']) {
        expect(card?.querySelector(sel(`delivery-count-${c}-${k}`)), `${c} ${k}`).not.toBeNull()
      }
    }
  })
})

describe('TrackingPage heading order (REQ-DSH-03 C7)', () => {
  // REQ-DSH-03 C7
  it('one h1 (page-title); in delivery-counters an h2 first, then one h3 per channel card, no h4', async () => {
    await mountWithEvents([
      ['snapshot', status(CAMPAIGN_ID, 'sending', [5, 2, 3, 1], [4, 0, 6, 0])],
    ])
    const h1 = document.querySelectorAll('h1')
    expect(h1).toHaveLength(1)
    expect(h1[0]?.getAttribute('data-testid')).toBe('page-title')
    const hs = headingsIn(counters())
    expect(hs[0]?.tagName).toBe('H2')
    expect(hs.filter((h) => !['H2', 'H3'].includes(h.tagName))).toHaveLength(0)
    for (const c of CHANNELS) {
      expect(q(`delivery-card-${c}`)?.querySelectorAll('h3'), c).toHaveLength(1)
    }
  })
})

describe('TrackingPage scheduled view (REQ-DSH-03 C8)', () => {
  // REQ-DSH-03 C8
  it('renders no heading and no counter element in delivery-counters; the header block stays', async () => {
    const at = '2026-01-15T13:00:00Z'
    const camp = campaign('scheduled', version(V1, 1), {
      approval: { id: V3, content_version_id: V1, approved_at: T0 },
      scheduled_at: at,
      sending_started_at: null,
      completed_at: null,
    } as Partial<Campaign>)
    await mountWithEvents(
      [
        [
          'snapshot',
          status(CAMPAIGN_ID, 'scheduled', ZERO, ZERO, {
            scheduled_at: at,
            sending_started_at: null,
          }),
        ],
      ],
      camp,
    )
    const root = counters()
    expect(root?.getAttribute('data-state')).toBe('scheduled')
    expect(headingsIn(root)).toHaveLength(0)
    const absent = [
      'delivery-percent',
      'delivery-processed',
      'delivery-count-total-total',
      'delivery-results',
      'delivery-remaining',
      'delivery-percent-sms',
      'delivery-percent-email',
      'delivery-live',
      'delivery-last-updated',
      'delivery-waiting',
      'delivery-stale-note',
    ]
    for (const id of absent) expect(q(id), id).toBeNull()
    expect(document.querySelectorAll('[data-testid^="delivery-count-"]')).toHaveLength(0)
    expect(document.querySelectorAll('[data-testid^="delivery-card-"]')).toHaveLength(0)
    expect(q('schedule-panel')?.querySelector('h2')).not.toBeNull()
    expect(q('tracking-campaign-brief')?.textContent).toBe(camp.brief)
  })
})

describe('TrackingPage variant cards (REQ-DSH-03 C10)', () => {
  const counts = (queued: number, sending: number, sent: number, failed: number) => ({
    total: queued + sending + sent + failed,
    queued,
    sending,
    sent,
    failed,
  })
  const BY_VARIANT = {
    a: { sms: counts(50, 5, 120, 2), email: counts(60, 4, 110, 1) },
    b: { sms: counts(50, 15, 110, 3), email: counts(140, 6, 50, 3) },
  }

  // REQ-DSH-03 C10
  it('variant-cards next to the table; every variant-* testid is on one element, 12 variant-count-* in the table', async () => {
    const c = {
      ...version().content,
      b: { sms: { body: 'B' }, email: { subject: 'B', body: 'B' } },
      angles: { a: 'İndirim oranı', b: 'Aciliyet' },
    }
    await mountWithEvents(
      [
        [
          'snapshot',
          status(CAMPAIGN_ID, 'sending', [100, 20, 230, 5], [200, 10, 160, 4], {
            by_variant: BY_VARIANT,
          }),
        ],
      ],
      campaign('sending', version(V1, 1, c as never)),
    )
    expect(q('variant-breakdown')?.tagName).toBe('TABLE')
    const cards = q('variant-cards')
    expect(cards).not.toBeNull()
    expect(document.querySelectorAll(sel('variant-cards'))).toHaveLength(1)
    expect(cards?.querySelectorAll('[data-testid^="variant-"]')).toHaveLength(0)
    expect(document.querySelectorAll('[data-testid^="variant-count-"]')).toHaveLength(12)
    const ids = [...document.querySelectorAll('[data-testid^="variant-"]')].map((e) =>
      e.getAttribute('data-testid'),
    )
    expect(new Set(ids).size).toBe(ids.length)
  })
})
