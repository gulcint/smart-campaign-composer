// Shared P5 tracking test data and fakes (docs/requirements.md REQ-TRK-01). Not a spec file.
//
// REQ-TRK-01 names the factory as `(url: string) => EventSourceLike` but does not list the members of
// `EventSourceLike`, so this fake implements the browser `EventSource` surface the page can rely on:
// `url`, `readyState` (0 connecting, 1 open, 2 closed), `close()`, `addEventListener` /
// `removeEventListener` and the `onopen` / `onerror` / `onmessage` properties. Tests drive it by hand.

export type Counts = [queued: number, sending: number, sent: number, failed: number]

export const CHANNELS = ['sms', 'email'] as const
export const KEYS = ['total', 'queued', 'sending', 'sent', 'failed'] as const

function chan([queued, sending, sent, failed]: Counts) {
  return { total: queued + sending + sent + failed, queued, sending, sent, failed }
}

/** A `CampaignStatus` body (REQ-TRK-01): per channel and summed counters, `total = q+s+se+f`. */
export function status(
  campaignId: string,
  state: string,
  sms: Counts,
  email: Counts,
  extra: Record<string, unknown> = {},
) {
  const s = chan(sms)
  const e = chan(email)
  return {
    campaign_id: campaignId,
    state,
    counts: {
      total: s.total + e.total,
      queued: s.queued + e.queued,
      sending: s.sending + e.sending,
      sent: s.sent + e.sent,
      failed: s.failed + e.failed,
    },
    by_channel: { sms: s, email: e },
    sending_started_at: '2026-01-15T12:00:00Z',
    completed_at: state === 'completed' ? '2026-01-15T12:05:00Z' : null,
    updated_at: '2026-01-15T12:00:00Z',
    ...extra,
  }
}

export type CampaignStatusBody = ReturnType<typeof status>

type Listener = EventListenerOrEventListenerObject

export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  readonly CONNECTING = 0
  readonly OPEN = 1
  readonly CLOSED = 2

  readyState = 0
  readonly withCredentials = false
  onopen: ((e: Event) => unknown) | null = null
  onerror: ((e: Event) => unknown) | null = null
  onmessage: ((e: MessageEvent) => unknown) | null = null
  closeCalls = 0
  private seq = 0
  private readonly listeners = new Map<string, Set<Listener>>()

  constructor(readonly url: string) {}

  addEventListener(type: string, l: Listener | null) {
    if (!l) return
    let set = this.listeners.get(type)
    if (!set) {
      set = new Set()
      this.listeners.set(type, set)
    }
    set.add(l)
  }

  removeEventListener(type: string, l: Listener | null) {
    if (l) this.listeners.get(type)?.delete(l)
  }

  dispatchEvent(e: Event): boolean {
    this.fire(e)
    return true
  }

  close() {
    this.closeCalls++
    this.readyState = 2
  }

  /** Listener count across all event types; 0 after the page detached itself. */
  listenerCount(): number {
    let n = 0
    for (const s of this.listeners.values()) n += s.size
    return n
  }

  // ---- test drivers ----

  simulateOpen() {
    this.readyState = 1
    this.fire(new Event('open'))
  }

  /** Fires `error`; `readyState` 2 means the browser gave up (the client must reconnect itself). */
  simulateError(readyState: 0 | 2 = 2) {
    this.readyState = readyState
    this.fire(new Event('error'))
  }

  /** Fires a named SSE event (`snapshot`, `status`, `completed`) with a one-line JSON `data`. */
  simulateEvent(type: string, data: unknown) {
    this.seq++
    this.fire(new MessageEvent(type, { data: JSON.stringify(data), lastEventId: String(this.seq) }))
  }

  private fire(e: Event) {
    for (const l of [...(this.listeners.get(e.type) ?? [])]) {
      if (typeof l === 'function') l.call(this, e)
      else l.handleEvent(e)
    }
    if (e.type === 'open') this.onopen?.(e)
    else if (e.type === 'error') this.onerror?.(e)
    else if (e.type === 'message') this.onmessage?.(e as MessageEvent)
  }
}

/** A factory to provide with `EventSourceFactoryKey`; records every stream it creates. */
export function fakeEventSourceFactory() {
  const created: FakeEventSource[] = []
  const factory = (url: string) => {
    const es = new FakeEventSource(url)
    created.push(es)
    return es
  }
  return {
    factory,
    created,
    /** The most recently created stream. */
    last(): FakeEventSource {
      const es = created[created.length - 1]
      if (!es) throw new Error('no EventSource was created')
      return es
    },
  }
}
