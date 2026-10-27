// REQ-CNT-05 (UI): an approval refused with 422 at approval time keeps the dialog open and lists
// the worst-case field errors. The dialog may be teleported to <body>, so elements are queried on
// document.
import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
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
