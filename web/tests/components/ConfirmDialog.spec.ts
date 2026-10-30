// REQ-CNT-05 (UI): an approval refused with 422 at approval time keeps the dialog open and lists
// the worst-case field errors. REQ-SCH-04 C3 (UI): the send dialog's "now / later" choice and the
// TSİ plan time with its window. The dialog may be teleported to <body>, so elements are queried on
// document.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import { t } from '@/i18n/index'
import { apiError, fieldError, sel } from '../helpers/p4'

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
})

async function mountDialog(props: Record<string, unknown> = {}) {
  wrapper = mount(ConfirmDialog, {
    props: { open: true, kind: 'approve', counts: null, submitting: false, error: null, ...props },
    attachTo: document.body,
  })
  await nextTick()
  await nextTick()
  return wrapper
}

const q = (id: string) => document.querySelector(sel(id)) as HTMLElement | null
const text = (id: string) => q(id)?.textContent?.trim()

describe('ConfirmDialog (REQ-CNT-05)', () => {
  it('an approval-time 422 stays open and lists the worst-case field errors (REQ-CNT-05)', async () => {
    const error = apiError(422, 'validation_failed', [
      fieldError('sms.body', 'sms_too_long_worst_case', 160, 163),
      fieldError('email.subject', 'subject_too_long_worst_case', 80, 82),
    ])
    await mountDialog({ error })
    const box = q('confirm-dialog-error')
    expect(box?.getAttribute('data-code')).toBe('validation_failed')
    const codes = [...(box?.querySelectorAll(sel('field-error')) ?? [])].map((e) =>
      e.getAttribute('data-code'),
    )
    expect(codes).toEqual(['sms_too_long_worst_case', 'subject_too_long_worst_case'])
    expect(q('confirm-dialog')).not.toBeNull()
  })
})

// ---- REQ-SCH-04 C3: planning in the send dialog ----

// "Now" is 2026-01-15T12:00:00Z = 15:00 TSİ; the datetime-local value is TSİ YYYY-MM-DDTHH:MM.
const NOW = '2026-01-15T12:00:00Z'

/** Types a datetime-local value the way a user does (input + change). */
async function fill(id: string, value: string) {
  const el = q(id) as HTMLInputElement | null
  if (!el) throw new Error(`${id} is not in the DOM`)
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await nextTick()
  await nextTick()
}

async function click(id: string) {
  q(id)?.click()
  await nextTick()
  await nextTick()
}

const PLAN_IDS = [
  'confirm-dialog-send-now',
  'confirm-dialog-send-later',
  'confirm-dialog-scheduled-at',
  'confirm-dialog-tz',
  'confirm-dialog-schedule-note',
  'confirm-dialog-schedule-error',
]

describe('ConfirmDialog send planning (REQ-SCH-04 C3)', () => {
  beforeEach(() => {
    // Date is faked and fixed BEFORE the dialog mounts or anything is typed.
    vi.useFakeTimers({
      toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'],
    })
    vi.setSystemTime(new Date(NOW))
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  const sendDialog = (props: Record<string, unknown> = {}) =>
    mountDialog({ kind: 'send', allowSchedule: true, ...props })

  it('opens in now mode: the now radio is checked, no plan input, confirm emits confirm once', async () => {
    const w = await sendDialog()
    expect(q('confirm-dialog')?.getAttribute('data-mode')).toBe('now')
    for (const [id, checked] of [
      ['confirm-dialog-send-now', 'true'],
      ['confirm-dialog-send-later', 'false'],
    ] as const) {
      expect(q(id)?.getAttribute('role'), id).toBe('radio')
      expect(q(id)?.getAttribute('aria-checked') ?? 'false', id).toBe(checked)
    }
    expect(q('confirm-dialog-scheduled-at')).toBeNull()
    expect(text('confirm-dialog-confirm')).toBe(t('confirm.send.confirm'))
    await click('confirm-dialog-confirm')
    expect(w.emitted('confirm')).toHaveLength(1)
    expect(w.emitted('schedule')).toBeUndefined()
  })

  it('later mode shows the TSİ input, the zone and the audience note; an empty value disables confirm', async () => {
    const w = await sendDialog()
    await click('confirm-dialog-send-later')
    expect(q('confirm-dialog')?.getAttribute('data-mode')).toBe('later')
    expect(q('confirm-dialog-send-later')?.getAttribute('aria-checked')).toBe('true')
    expect(q('confirm-dialog-send-now')?.getAttribute('aria-checked') ?? 'false').toBe('false')
    const input = q('confirm-dialog-scheduled-at') as HTMLInputElement | null
    expect(input?.getAttribute('type')).toBe('datetime-local')
    expect(input?.value).toBe('')
    expect(input?.hasAttribute('data-value')).toBe(false)
    expect(text('confirm-dialog-tz')).toBe(t('schedule.tz'))
    expect(text('confirm-dialog-schedule-note')).toBe(t('confirm.schedule.audienceNote'))
    expect(text('confirm-dialog-title')).toBe(t('confirm.schedule.title'))
    expect(text('confirm-dialog-confirm')).toBe(t('confirm.schedule.confirm'))
    expect(q('confirm-dialog-confirm')?.getAttribute('aria-disabled')).toBe('true')
    expect(q('confirm-dialog-schedule-error')).toBeNull()
    await click('confirm-dialog-confirm')
    expect(w.emitted('schedule')).toBeUndefined()
    expect(w.emitted('confirm')).toBeUndefined()
  })

  it.each([
    {
      name: 'exactly now + 60 s is valid (bound included)',
      value: '2026-01-15T15:01',
      utc: '2026-01-15T12:01:00Z',
    },
    { name: 'an hour ahead is valid', value: '2026-01-15T16:30', utc: '2026-01-15T13:30:00Z' },
    {
      name: 'exactly now + 30 days is valid',
      value: '2026-02-14T15:00',
      utc: '2026-02-14T12:00:00Z',
    },
    { name: 'after TSİ midnight', value: '2026-01-16T00:05', utc: '2026-01-15T21:05:00Z' },
    {
      name: 'min lead 0 accepts now',
      value: '2026-01-15T15:00',
      utc: '2026-01-15T12:00:00Z',
      props: { scheduleMinLeadSeconds: 0 },
    },
    {
      name: 'a custom max of 1 h accepts exactly 1 h',
      value: '2026-01-15T16:00',
      utc: '2026-01-15T13:00:00Z',
      props: { scheduleMaxAheadSeconds: 3600 },
    },
  ])(
    '$name: data-value is the UTC ISO and confirm emits schedule once',
    async ({ value, utc, props }) => {
      const w = await sendDialog(props ?? {})
      await click('confirm-dialog-send-later')
      await fill('confirm-dialog-scheduled-at', value)
      expect(q('confirm-dialog-scheduled-at')?.getAttribute('data-value')).toBe(utc)
      expect(q('confirm-dialog-schedule-error')).toBeNull()
      expect(q('confirm-dialog-scheduled-at')?.getAttribute('aria-invalid') ?? 'false').not.toBe(
        'true',
      )
      expect(q('confirm-dialog-confirm')?.getAttribute('aria-disabled') ?? 'false').not.toBe('true')
      await click('confirm-dialog-confirm')
      expect(w.emitted('schedule')).toEqual([[utc]])
      expect(w.emitted('confirm')).toBeUndefined()
    },
  )

  it.each([
    {
      name: '1 min before now + 60 s is too soon',
      value: '2026-01-15T15:00',
      utc: '2026-01-15T12:00:00Z',
      code: 'schedule_too_soon',
    },
    {
      name: 'the past is too soon',
      value: '2026-01-14T09:00',
      utc: '2026-01-14T06:00:00Z',
      code: 'schedule_too_soon',
    },
    {
      name: 'a custom min lead of 10 min',
      value: '2026-01-15T15:09',
      utc: '2026-01-15T12:09:00Z',
      code: 'schedule_too_soon',
      props: { scheduleMinLeadSeconds: 600 },
    },
    {
      name: 'one minute past now + 30 days is too far',
      value: '2026-02-14T15:01',
      utc: '2026-02-14T12:01:00Z',
      code: 'schedule_too_far',
    },
    {
      name: 'past a custom max of 1 h',
      value: '2026-01-15T16:01',
      utc: '2026-01-15T13:01:00Z',
      code: 'schedule_too_far',
      props: { scheduleMaxAheadSeconds: 3600 },
    },
  ])('$name: the error blocks confirm', async ({ value, utc, code, props }) => {
    const w = await sendDialog(props ?? {})
    await click('confirm-dialog-send-later')
    await fill('confirm-dialog-scheduled-at', value)
    expect(q('confirm-dialog-scheduled-at')?.getAttribute('data-value')).toBe(utc)
    const err = q('confirm-dialog-schedule-error')
    expect(err?.getAttribute('data-code')).toBe(code)
    if (code === 'schedule_too_soon') {
      expect(err?.textContent?.trim()).toBe(t('field_error.schedule_too_soon'))
    }
    expect(q('confirm-dialog-scheduled-at')?.getAttribute('aria-invalid')).toBe('true')
    expect(q('confirm-dialog-confirm')?.getAttribute('aria-disabled')).toBe('true')
    await click('confirm-dialog-confirm')
    expect(w.emitted('schedule')).toBeUndefined()
    expect(w.emitted('confirm')).toBeUndefined()
  })

  it('switching back to now confirms as before', async () => {
    const w = await sendDialog()
    await click('confirm-dialog-send-later')
    await fill('confirm-dialog-scheduled-at', '2026-01-15T16:30')
    await click('confirm-dialog-send-now')
    expect(q('confirm-dialog')?.getAttribute('data-mode')).toBe('now')
    expect(text('confirm-dialog-confirm')).toBe(t('confirm.send.confirm'))
    await click('confirm-dialog-confirm')
    expect(w.emitted('confirm')).toHaveLength(1)
    expect(w.emitted('schedule')).toBeUndefined()
  })

  it('allowSchedule = false renders none of the plan elements', async () => {
    await mountDialog({ kind: 'send', allowSchedule: false })
    expect(q('confirm-dialog')).not.toBeNull()
    for (const id of PLAN_IDS) expect(q(id), id).toBeNull()
  })
})
