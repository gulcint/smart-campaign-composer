// REQ-TRK-01 (UI): TrackingPage live counters, connection state and stream client (docs/requirements.md
// REQ-TRK-01: mount surface `@/pages/TrackingPage.vue`, route `tracking`, `EventSourceFactoryKey` and
// `ApiClientKey` injection, reconnect rules, `completed` closing the stream, 404 without a stream).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import TrackingPage from '@/pages/TrackingPage.vue'
import { mountPage } from '../helpers/pages'
import { CAMPAIGN_ID, apiError, campaign, fakeApi, sel, version } from '../helpers/p4'
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
