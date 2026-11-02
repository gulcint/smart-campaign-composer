// REQ-DSH-02 C15, REQ-DSH-03 C9 (UI): `messages` of `@/i18n/index.ts` holds a non-empty text for every
// id of the brief-page campaign panel and the tracking layout. An id is looked up as a flat key
// first and then as a dotted path (same rule as variant-messages.spec.ts).
import { describe, expect, it } from 'vitest'
import { messages } from '@/i18n/index'

const PANEL_IDS = [
  'briefPanel.title',
  'briefPanel.kpi.total',
  'briefPanel.kpi.scheduled',
  'briefPanel.kpi.sent',
  'briefPanel.kpi.messagesSent',
  'briefPanel.kpi.messagesFailed',
  'briefPanel.scheduledTitle',
  'briefPanel.sentTitle',
  'briefPanel.scheduledEmpty',
  'briefPanel.sentEmpty',
  'briefPanel.more',
  'briefPanel.moreLoaded',
  'briefPanel.scheduledError',
  'briefPanel.sentError',
  'briefPanel.retry',
  'briefPanel.sentCounts',
  'app.seedNote',
  'schedule.tzNote',
]

const TRACKING_IDS = [
  'tracking.overallTitle',
  'tracking.processed',
  'tracking.results',
  'tracking.remaining',
  'tracking.startedAt',
  'tracking.completedAt',
  'tracking.audience',
  'delivery.live',
  'delivery.lastUpdated',
  'delivery.waiting',
  'delivery.completedTitle',
  'schedule.tzNote',
]

function lookup(id: string): unknown {
  const root = messages as unknown as Record<string, unknown>
  if (typeof root[id] === 'string') return root[id]
  let cur: unknown = root
  for (const part of id.split('.')) {
    if (cur === null || typeof cur !== 'object') return undefined
    cur = (cur as Record<string, unknown>)[part]
  }
  return cur
}

describe('campaign panel messages (REQ-DSH-02 C15)', () => {
  // REQ-DSH-02 C15
  it.each(PANEL_IDS)('%s has a non-empty text', (id) => {
    const v = lookup(id)
    expect(typeof v, id).toBe('string')
    expect((v as string).trim().length, id).toBeGreaterThan(0)
  })
})

describe('tracking layout messages (REQ-DSH-03 C9)', () => {
  // REQ-DSH-03 C9
  it.each(TRACKING_IDS)('%s has a non-empty text', (id) => {
    const v = lookup(id)
    expect(typeof v, id).toBe('string')
    expect((v as string).trim().length, id).toBeGreaterThan(0)
  })
})
