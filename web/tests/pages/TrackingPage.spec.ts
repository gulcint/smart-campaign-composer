// REQ-TRK-01 (UI): TrackingPage live counters, connection state and stream client (docs/requirements.md
// REQ-TRK-01: mount surface `@/pages/TrackingPage.vue`, route `tracking`, `EventSourceFactoryKey` and
// `ApiClientKey` injection, reconnect rules, `completed` closing the stream, 404 without a stream).
//
// REQ-SCH-04 C7, C8, C9 (UI): the waiting plan (schedule-panel, countdown), cancel and "send now"
// from the tracking page, and the started-from-plan note.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import TrackingPage from '@/pages/TrackingPage.vue'
import type { Campaign } from '@/api/types'
import { t } from '@/i18n/index'
import { formatCountdown, formatTsi } from '@/lib/schedule'
import { mountPage } from '../helpers/pages'
import { CAMPAIGN_ID, T0, V1, V3, apiError, campaign, fakeApi, sel, version } from '../helpers/p4'
import {
  CHANNELS,
  KEYS,
  fakeEventSourceFactory,
  status,
  type CampaignStatusBody,
} from '../helpers/p5'

const C = `/campaigns/${CAMPAIGN_ID}`
const EVENTS_URL = `/api/campaigns/${CAMPAIGN_ID}/events`
const UNKNOWN_ID = '3f2e1d0c-9b8a-4c7d-8e6f-5a4b3c2d1e0f'

let wrapper: VueWrapper | undefined

beforeEach(() => {
  // REQ-TRK-01: "zamanlayıcılar için vi.useFakeTimers() kullanır". setImmediate / microtasks stay real so
  // flushPromises() keeps working.
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
  vi.useRealTimers()
})

const q = (id: string) => document.querySelector(sel(id)) as HTMLElement | null

function deliveriesPage() {
  return { items: [], total: 0, limit: 50, offset: 0 }
}

async function mountTracking() {
  const es = fakeEventSourceFactory()
  const a = fakeApi({
    [`GET ${C}`]: () => campaign('sending', version()),
    [`GET ${C}/deliveries`]: () => deliveriesPage(),
  })
  const r = await mountPage(TrackingPage, 'tracking', { api: a.client, eventSource: es.factory })
  wrapper = r.wrapper
  return { es, a, router: r.router, w: r.wrapper }
}

/** Every `delivery-count-<c>-<k>` and `delivery-count-total-<k>` carries the matching counter. */
function expectCounters(st: CampaignStatusBody) {
  for (const k of KEYS) {
    expect(q(`delivery-count-total-${k}`)?.getAttribute('data-value'), `total ${k}`).toBe(
      String(st.counts[k]),
    )
    for (const c of CHANNELS) {
      expect(q(`delivery-count-${c}-${k}`)?.getAttribute('data-value'), `${c} ${k}`).toBe(
        String(st.by_channel[c][k]),
      )
    }
  }
}

const countersState = () => q('delivery-counters')?.getAttribute('data-state')

const SNAP = status(CAMPAIGN_ID, 'sending', [300, 20, 30, 0], [350, 10, 8, 0])
const MID = status(CAMPAIGN_ID, 'sending', [100, 15, 230, 5], [200, 12, 154, 2])
const DONE_FAILED = status(CAMPAIGN_ID, 'completed', [0, 0, 342, 8], [0, 0, 365, 3])
const DONE_CLEAN = status(CAMPAIGN_ID, 'completed', [0, 0, 350, 0], [0, 0, 368, 0])

describe('TrackingPage stream (REQ-TRK-01)', () => {
  it('opens exactly one stream at /api/campaigns/{id}/events through the provided factory', async () => {
    const { es, w } = await mountTracking()
    expect(w.find(sel('page-tracking')).exists()).toBe(true)
    expect(es.created).toHaveLength(1)
    expect(es.last().url).toBe(EVENTS_URL)
  })

  it('snapshot, status and completed update every counter without a reload', async () => {
    const { es, router } = await mountTracking()
    const s = es.last()
    s.simulateOpen()
    s.simulateEvent('snapshot', SNAP)
    await flushPromises()
    expectCounters(SNAP)

    s.simulateEvent('status', MID)
    await flushPromises()
    expectCounters(MID)

    s.simulateEvent('completed', DONE_FAILED)
    await flushPromises()
    expectCounters(DONE_FAILED)

    expect(router.currentRoute.value.name).toBe('tracking')
    expect(es.created).toHaveLength(1)
  })

  it('a snapshot after reconnecting replaces all counters', async () => {
    const { es } = await mountTracking()
    const first = es.last()
    first.simulateOpen()
    first.simulateEvent('snapshot', SNAP)
    first.simulateEvent('status', MID)
    await flushPromises()
    first.simulateError(2)
    await vi.advanceTimersByTimeAsync(1000)
    expect(es.created).toHaveLength(2)

    const again = status(CAMPAIGN_ID, 'sending', [10, 1, 330, 9], [20, 2, 340, 6])
    const second = es.last()
    second.simulateOpen()
    second.simulateEvent('snapshot', again)
    await flushPromises()
    expectCounters(again)
  })
})

describe('TrackingPage delivery-counters data-state (REQ-TRK-01)', () => {
  it('is live while the stream is open', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    await flushPromises()
    expect(countersState()).toBe('live')
  })

  it.each([
    { name: 'closed by the browser (readyState 2)', readyState: 2 as const },
    { name: 'reconnecting (readyState 0)', readyState: 0 as const },
  ])('is stale after an error when the stream is $name', async ({ readyState }) => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    await flushPromises()
    es.last().simulateError(readyState)
    await flushPromises()
    expect(countersState()).toBe('stale')
  })

  it('goes back to live after a successful reconnect', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    await flushPromises()
    es.last().simulateError(2)
    await vi.advanceTimersByTimeAsync(1000)
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', MID)
    await flushPromises()
    expect(countersState()).toBe('live')
  })

  it('completed outranks stale: after completed the closed stream still shows completed', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    es.last().simulateEvent('completed', DONE_FAILED)
    await flushPromises()
    expect(es.last().readyState).toBe(2)
    expect(countersState()).toBe('completed')
  })
})

describe('TrackingPage delivery-stale-note (REQ-TRK-01)', () => {
  it('is not shown during the very first connect before any status arrived', async () => {
    const { es } = await mountTracking()
    expect(es.last().readyState).toBe(0)
    await flushPromises()
    expect(q('delivery-stale-note')).toBeNull()
  })

  it('is shown when the very first connect attempt fails', async () => {
    const { es } = await mountTracking()
    es.last().simulateError(2)
    await flushPromises()
    expect(q('delivery-stale-note')).not.toBeNull()
  })

  it('is shown after an error once a status was received', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    await flushPromises()
    expect(q('delivery-stale-note')).toBeNull()
    es.last().simulateError(2)
    await flushPromises()
    expect(q('delivery-stale-note')).not.toBeNull()
  })

  it('disappears again once the reconnected stream is live', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    es.last().simulateError(2)
    await vi.advanceTimersByTimeAsync(1000)
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', MID)
    await flushPromises()
    expect(q('delivery-stale-note')).toBeNull()
  })
})

describe('TrackingPage completed view (REQ-TRK-01)', () => {
  it('shows neither the completed title nor the stale note while sending live', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    await flushPromises()
    expect(q('delivery-completed-title')).toBeNull()
    expect(q('delivery-stale-note')).toBeNull()
  })

  it.each([
    { name: 'failed > 0 shows the failed link', st: DONE_FAILED, link: true },
    { name: 'failed = 0 hides the failed link', st: DONE_CLEAN, link: false },
  ])('completed with $name', async ({ st, link }) => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    es.last().simulateEvent('completed', st)
    await flushPromises()
    expect(q('delivery-completed-title')).not.toBeNull()
    expect(q('delivery-failed-link') !== null).toBe(link)
    expect(q('delivery-stale-note')).toBeNull()
  })

  it('a campaign already completed on connect shows completed after snapshot then completed', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', DONE_FAILED)
    es.last().simulateEvent('completed', DONE_FAILED)
    await flushPromises()
    expectCounters(DONE_FAILED)
    expect(countersState()).toBe('completed')
    expect(q('delivery-completed-title')).not.toBeNull()
    expect(q('delivery-failed-link')).not.toBeNull()
  })
})

describe('TrackingPage reconnect backoff (REQ-TRK-01)', () => {
  it('reconnects after 1 s, 2 s, 4 s, 8 s, 16 s, then at most every 30 s', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    await flushPromises()

    const waits = [1000, 2000, 4000, 8000, 16000, 30000, 30000]
    for (const [i, wait] of waits.entries()) {
      const before = es.created.length
      es.last().simulateError(2)
      await vi.advanceTimersByTimeAsync(wait - 1)
      expect(es.created.length, `attempt ${i + 1}: no stream before ${wait} ms`).toBe(before)
      await vi.advanceTimersByTimeAsync(1)
      expect(es.created.length, `attempt ${i + 1}: new stream at ${wait} ms`).toBe(before + 1)
      expect(es.last().url).toBe(EVENTS_URL)
    }
  })

  it('a successful open resets the wait to 1 s', async () => {
    const { es } = await mountTracking()
    es.last().simulateOpen()
    // Three failed attempts grow the wait to 1 s, 2 s, 4 s.
    for (const wait of [1000, 2000, 4000]) {
      es.last().simulateError(2)
      await vi.advanceTimersByTimeAsync(wait)
    }
    expect(es.created).toHaveLength(4)

    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', SNAP)
    await flushPromises()
    es.last().simulateError(2)
    await vi.advanceTimersByTimeAsync(999)
    expect(es.created).toHaveLength(4)
    await vi.advanceTimersByTimeAsync(1)
    expect(es.created).toHaveLength(5)
  })

  it('the first connect failing waits 1 s before the next attempt', async () => {
    const { es } = await mountTracking()
    es.last().simulateError(2)
    await vi.advanceTimersByTimeAsync(999)
    expect(es.created).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(es.created).toHaveLength(2)
  })
})

describe('TrackingPage stream lifecycle (REQ-TRK-01)', () => {
  it('completed closes the stream and never reconnects', async () => {
    const { es } = await mountTracking()
    const s = es.last()
    s.simulateOpen()
    s.simulateEvent('snapshot', SNAP)
    s.simulateEvent('completed', DONE_CLEAN)
    await flushPromises()
    expect(s.closeCalls).toBeGreaterThanOrEqual(1)
    expect(s.readyState).toBe(2)
    await vi.advanceTimersByTimeAsync(120_000)
    expect(es.created).toHaveLength(1)
  })

  it('an unknown campaign (404) creates no EventSource and shows no connection-banner', async () => {
    const es = fakeEventSourceFactory()
    const a = fakeApi({
      [`GET /campaigns/${UNKNOWN_ID}`]: () => {
        throw apiError(404, 'campaign_not_found')
      },
      [`GET /campaigns/${UNKNOWN_ID}/deliveries`]: () => {
        throw apiError(404, 'campaign_not_found')
      },
    })
    const r = await mountPage(TrackingPage, 'tracking', {
      api: a.client,
      eventSource: es.factory,
      campaignId: UNKNOWN_ID,
    })
    wrapper = r.wrapper
    await flushPromises()
    expect(es.created).toHaveLength(0)
    expect(q('connection-banner')).toBeNull()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(es.created).toHaveLength(0)
    expect(q('connection-banner')).toBeNull()
  })

  it('unmount closes the open stream and does not reconnect', async () => {
    const { es, w } = await mountTracking()
    const s = es.last()
    s.simulateOpen()
    s.simulateEvent('snapshot', SNAP)
    await flushPromises()
    w.unmount()
    wrapper = undefined
    expect(s.closeCalls).toBeGreaterThanOrEqual(1)
    expect(s.readyState).toBe(2)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(es.created).toHaveLength(1)
  })

  it('unmount during a reconnect wait cancels the pending reconnect', async () => {
    const { es, w } = await mountTracking()
    es.last().simulateOpen()
    es.last().simulateError(2)
    await flushPromises()
    w.unmount()
    wrapper = undefined
    await vi.advanceTimersByTimeAsync(60_000)
    expect(es.created).toHaveLength(1)
  })
})

// ---- REQ-SCH-04 C7, C8, C9: a planned send on the tracking page ----

// Now = 2026-01-15T12:00:00Z, fixed (Date faked) before mounting; the plan is 90 s ahead.
const NOW = '2026-01-15T12:00:00Z'
const AT_90S = '2026-01-15T12:01:30Z'
const ZERO = [0, 0, 0, 0] as [number, number, number, number]

function plannedCampaign(state: string, at: string | null): Campaign {
  return campaign(state, version(V1, 1), {
    approval: { id: V3, content_version_id: V1, approved_at: T0 },
    scheduled_at: at,
    sending_started_at: state === 'sending' || state === 'completed' ? (at ?? T0) : null,
  } as Partial<Campaign>)
}

function planStatus(state: string, at: string | null, sms = ZERO, email = ZERO) {
  return status(CAMPAIGN_ID, state, sms, email, {
    scheduled_at: at,
    sending_started_at: state === 'scheduled' || state === 'approved' ? null : (at ?? T0),
  })
}

interface PlanSetup {
  camp: { value: Campaign }
  cancel?: () => unknown
  send?: () => unknown
}

async function mountPlanned(s: PlanSetup) {
  const es = fakeEventSourceFactory()
  const a = fakeApi({
    [`GET ${C}`]: () => s.camp.value,
    [`GET ${C}/deliveries`]: () => deliveriesPage(),
    [`DELETE ${C}/schedule`]: () =>
      s.cancel ? s.cancel() : { campaign_id: CAMPAIGN_ID, state: 'approved', scheduled_at: null },
    [`POST ${C}/send`]: () =>
      s.send
        ? s.send()
        : {
            campaign_id: CAMPAIGN_ID,
            state: 'sending',
            content_version_id: V1,
            scheduled_at: null,
            deliveries: { total: 718, sms: 350, email: 368 },
          },
  })
  const r = await mountPage(TrackingPage, 'tracking', {
    api: a.client,
    eventSource: es.factory,
    withToaster: true,
  })
  wrapper = r.wrapper
  return { es, a, router: r.router }
}

function toastWith(messageId: string): HTMLElement | undefined {
  return [...document.querySelectorAll(sel('toast'))].find(
    (e) => e.getAttribute('data-message-id') === messageId,
  ) as HTMLElement | undefined
}

const panelState = () => q('schedule-panel')?.getAttribute('data-state')
const countdown = () => q('schedule-countdown')

describe('TrackingPage waiting plan (REQ-SCH-04 C7)', () => {
  beforeEach(() => {
    vi.useFakeTimers({
      toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'],
    })
    vi.setSystemTime(new Date(NOW))
  })

  it('a scheduled campaign shows schedule-panel instead of the counters, and keeps the stream open', async () => {
    const { es } = await mountPlanned({ camp: { value: plannedCampaign('scheduled', AT_90S) } })
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', planStatus('scheduled', AT_90S))
    await flushPromises()

    expect(panelState()).toBe('waiting')
    expect(countersState()).toBe('scheduled')
    expect(
      q('delivery-counters')?.querySelectorAll('[data-testid^="delivery-count-"]'),
    ).toHaveLength(0)
    expect(q('activity-feed')).toBeNull()
    expect(q('schedule-time')?.getAttribute('data-value')).toBe(AT_90S)
    expect(q('schedule-time')?.textContent?.trim()).toBe(formatTsi(AT_90S))
    expect(countdown()?.getAttribute('data-value')).toBe('90')
    expect(countdown()?.textContent?.trim()).toBe(
      t('schedule.countdown', { hms: formatCountdown(90).hms }),
    )
    expect(es.created).toHaveLength(1)
    expect(es.last().closeCalls).toBe(0)
  })

  it('the countdown updates every 1000 ms and reaches starting at zero', async () => {
    const { es } = await mountPlanned({ camp: { value: plannedCampaign('scheduled', AT_90S) } })
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', planStatus('scheduled', AT_90S))
    await flushPromises()
    expect(countdown()?.getAttribute('data-value')).toBe('90')

    await vi.advanceTimersByTimeAsync(1000)
    expect(countdown()?.getAttribute('data-value')).toBe('89')
    expect(countdown()?.textContent?.trim()).toBe(
      t('schedule.countdown', { hms: formatCountdown(89).hms }),
    )

    await vi.advanceTimersByTimeAsync(89_000)
    expect(countdown()?.getAttribute('data-value')).toBe('0')
    expect(countdown()?.textContent?.trim()).toBe(t('schedule.starting'))
    expect(panelState()).toBe('starting')
  })

  it('a plan a day or more ahead shows the countdown with days', async () => {
    const at = '2026-01-17T13:01:01Z' // 2 d 01:01:01 ahead
    const { es } = await mountPlanned({ camp: { value: plannedCampaign('scheduled', at) } })
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', planStatus('scheduled', at))
    await flushPromises()
    const remaining = 2 * 86400 + 3661
    expect(countdown()?.getAttribute('data-value')).toBe(String(remaining))
    const { days, hms } = formatCountdown(remaining)
    expect(countdown()?.textContent?.trim()).toBe(t('schedule.countdownDays', { d: days, hms }))
  })

  it('a status event with state sending removes the panel and the counters go live', async () => {
    const { es } = await mountPlanned({ camp: { value: plannedCampaign('scheduled', AT_90S) } })
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', planStatus('scheduled', AT_90S))
    await flushPromises()
    es.last().simulateEvent(
      'status',
      planStatus('sending', AT_90S, [340, 10, 0, 0], [360, 8, 0, 0]),
    )
    await flushPromises()
    expect(q('schedule-panel')).toBeNull()
    expect(countersState()).toBe('live')
    expect(q('activity-feed')).toBeNull()
  })

  it('a stream event with state approved (plan canceled elsewhere) goes to the approval route', async () => {
    const setup: PlanSetup = { camp: { value: plannedCampaign('scheduled', AT_90S) } }
    const { es, router } = await mountPlanned(setup)
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', planStatus('scheduled', AT_90S))
    await flushPromises()
    setup.camp.value = plannedCampaign('approved', null)
    es.last().simulateEvent('status', planStatus('approved', null))
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('approval'))
  })
})

describe('TrackingPage plan actions (REQ-SCH-04 C8)', () => {
  beforeEach(() => {
    vi.useFakeTimers({
      toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'],
    })
    vi.setSystemTime(new Date(NOW))
  })

  async function waiting(setup: PlanSetup) {
    const m = await mountPlanned(setup)
    m.es.last().simulateOpen()
    m.es.last().simulateEvent('snapshot', planStatus('scheduled', AT_90S))
    await flushPromises()
    expect(panelState()).toBe('waiting')
    return m
  }

  it('schedule-cancel 200: DELETE, toast.scheduleCanceled, then the approval route', async () => {
    const setup: PlanSetup = { camp: { value: plannedCampaign('scheduled', AT_90S) } }
    setup.cancel = () => {
      setup.camp.value = plannedCampaign('approved', null)
      return { campaign_id: CAMPAIGN_ID, state: 'approved', scheduled_at: null }
    }
    const { a, router } = await waiting(setup)
    q('schedule-cancel')?.click()
    await flushPromises()
    expect(a.count('DELETE', `${C}/schedule`)).toBe(1)
    await vi.waitFor(() => expect(toastWith('toast.scheduleCanceled')).toBeDefined())
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('approval'))
  })

  it('schedule-cancel 409 invalid_state: error toast first, then a reload; a started send stays on tracking with counters', async () => {
    const setup: PlanSetup = { camp: { value: plannedCampaign('scheduled', AT_90S) } }
    setup.cancel = () => {
      // The worker started the plan just before the cancel arrived.
      setup.camp.value = plannedCampaign('sending', AT_90S)
      throw apiError(409, 'invalid_state')
    }
    const { a, router } = await waiting(setup)
    const gets = a.count('GET', C)
    q('schedule-cancel')?.click()
    await flushPromises()
    expect(a.count('DELETE', `${C}/schedule`)).toBe(1)
    await vi.waitFor(() => expect(toastWith('error.invalid_state')).toBeDefined())
    await vi.waitFor(() => expect(a.count('GET', C)).toBeGreaterThan(gets))
    // The reload comes after the refused DELETE.
    const order = a.calls.map((c) => `${c.method} ${c.path}`)
    expect(order.lastIndexOf(`GET ${C}`)).toBeGreaterThan(order.indexOf(`DELETE ${C}/schedule`))
    await vi.waitFor(() => expect(q('schedule-panel')).toBeNull())
    expect(router.currentRoute.value.name).toBe('tracking')
    expect(countersState()).not.toBe('scheduled')
  })

  it('schedule-send-now opens the send dialog in now mode without plan elements and POSTs only content_version_id', async () => {
    const { a, router } = await waiting({ camp: { value: plannedCampaign('scheduled', AT_90S) } })
    q('schedule-send-now')?.click()
    await flushPromises()
    await vi.waitFor(() => expect(q('confirm-dialog')?.getAttribute('data-kind')).toBe('send'))
    expect(q('confirm-dialog')?.getAttribute('data-mode')).toBe('now')
    for (const id of [
      'confirm-dialog-send-now',
      'confirm-dialog-send-later',
      'confirm-dialog-scheduled-at',
    ]) {
      expect(q(id), id).toBeNull()
    }
    q('confirm-dialog-confirm')?.click()
    await flushPromises()
    expect(a.count('POST', `${C}/send`)).toBe(1)
    expect(a.last('POST', `${C}/send`)?.opts?.body).toEqual({ content_version_id: V1 })
    expect(router.currentRoute.value.name).toBe('tracking')
    await vi.waitFor(() => expect(q('schedule-panel')).toBeNull())
  })
})

describe('TrackingPage started-from-plan note (REQ-SCH-04 C9)', () => {
  beforeEach(() => {
    vi.useFakeTimers({
      toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'],
    })
    vi.setSystemTime(new Date('2026-01-15T12:05:00Z'))
  })

  it.each([
    {
      name: 'sending from a plan shows it',
      state: 'sending',
      at: '2026-01-15T12:01:30Z' as string | null,
    },
    {
      name: 'completed from a plan shows it',
      state: 'completed',
      at: '2026-01-15T12:01:30Z' as string | null,
    },
    { name: 'an instant send has none', state: 'sending', at: null as string | null },
  ])('$name', async ({ state, at }) => {
    const { es } = await mountPlanned({ camp: { value: plannedCampaign(state, at) } })
    es.last().simulateOpen()
    es.last().simulateEvent('snapshot', planStatus(state, at, [0, 0, 350, 0], [0, 0, 368, 0]))
    await flushPromises()
    const note = q('schedule-started-note')
    if (at === null) {
      expect(note).toBeNull()
      return
    }
    expect(note?.getAttribute('data-value')).toBe(at)
    expect(note?.textContent?.trim()).toBe(t('schedule.startedNote', { time: formatTsi(at) }))
  })
})
