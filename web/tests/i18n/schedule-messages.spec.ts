// REQ-SCH-04 C10 (UI): `messages` of `@/i18n/index.ts` holds a non-empty text for every id the
// scheduling screens use. The criterion names the ids, not the object's layout, so an id is looked
// up as a flat key first and then as a dotted path.
import { describe, expect, it } from 'vitest'
import { messages } from '@/i18n/index'

const IDS = [
  'status.campaign.scheduled',
  'confirm.schedule.title',
  'confirm.schedule.body',
  'confirm.schedule.confirm',
  'confirm.schedule.audienceNote',
  'confirm.sendMode.label',
  'confirm.sendMode.now',
  'confirm.sendMode.later',
  'confirm.scheduledAt.label',
  'confirm.scheduledAt.hint',
  'schedule.tz',
  'schedule.title',
  'schedule.timeLabel',
  'schedule.countdown',
  'schedule.countdownDays',
  'schedule.starting',
  'schedule.cancel',
  'schedule.sendNow',
  'schedule.startedNote',
  'approval.scheduledNote',
  'approval.toTracking',
  'toast.scheduled',
  'toast.scheduleCanceled',
  'field_error.schedule_too_soon',
  'field_error.schedule_too_far',
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

describe('scheduling messages (REQ-SCH-04 C10)', () => {
  it.each(IDS)('%s has a non-empty text', (id) => {
    const v = lookup(id)
    expect(typeof v, id).toBe('string')
    expect((v as string).trim().length, id).toBeGreaterThan(0)
  })
})
