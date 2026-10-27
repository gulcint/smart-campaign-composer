// Live delivery status over GET + EventSource (docs/contracts/ui.md §5.3, sse-events.md §2).
// The browser reconnects by itself while readyState is CONNECTING; once the source is CLOSED
// (readyState 2) this composable reconnects with backoff 1 s, ×2 per failed attempt, max 30 s.
import type { Ref } from 'vue'
import { getCurrentScope, inject, onScopeDispose, ref, watch } from 'vue'

import type { EventSourceLike } from '@/api/client'
import { EventSourceFactoryKey } from '@/api/client'
import type { CampaignStatus, ConnectionState } from '@/api/types'

const BACKOFF_START_MS = 1000
const BACKOFF_MAX_MS = 30_000

export function useCampaignStatusStream(campaignId: Ref<string>) {
  const factory =
    inject(EventSourceFactoryKey, null) ??
    ((url: string) => new EventSource(url) as unknown as EventSourceLike)

  const status = ref<CampaignStatus | null>(null)
  const connection = ref<ConnectionState>('idle')
  const lastUpdatedAt = ref<string | null>(null)
  const completed = ref(false)

  let source: EventSourceLike | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let backoff = BACKOFF_START_MS
  let stopped = false

  function apply(data: string): CampaignStatus | null {
    let next: CampaignStatus
    try {
      next = JSON.parse(data) as CampaignStatus
    } catch {
      return null // a broken frame changes nothing; the next poll brings a full status again
    }
    status.value = next
    lastUpdatedAt.value = next.updated_at
    return next
  }

  function dropSource() {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
    if (source) {
      source.onopen = null
      source.onerror = null
      source.close()
      source = null
    }
  }

  function connect() {
    dropSource()
    const id = campaignId.value
    if (stopped || completed.value || !id) return
    const es = factory(`/api/campaigns/${encodeURIComponent(id)}/events`)
    source = es
    connection.value = 'connecting'
    es.onopen = () => {
      if (source !== es) return
      backoff = BACKOFF_START_MS
      connection.value = 'open'
    }
    es.onerror = () => {
      if (source !== es || completed.value) return
      if (es.readyState !== 2) {
        connection.value = 'connecting' // the browser retries on its own (retry: 3000)
        return
      }
      dropSource()
      connection.value = 'error'
      const wait = backoff
      backoff = Math.min(backoff * 2, BACKOFF_MAX_MS)
      timer = setTimeout(() => {
        timer = null
        connect()
      }, wait)
    }
    const onStatus = (ev: { data: string }) => {
      if (source === es) apply(ev.data)
    }
    es.addEventListener('snapshot', onStatus)
    es.addEventListener('status', onStatus)
    es.addEventListener('completed', (ev) => {
      if (source !== es) return
      apply(ev.data)
      completed.value = true
      close()
    })
  }

  function close() {
    stopped = true
    dropSource()
    connection.value = 'closed'
  }

  // A different campaign starts from scratch (a reconnect's snapshot replaces everything anyway).
  watch(
    campaignId,
    () => {
      stopped = false
      completed.value = false
      status.value = null
      lastUpdatedAt.value = null
      backoff = BACKOFF_START_MS
      connect()
    },
    { immediate: true },
  )

  if (getCurrentScope()) onScopeDispose(close)

  return { status, connection, lastUpdatedAt, completed, close }
}
