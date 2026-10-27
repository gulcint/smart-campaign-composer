// REQ-APR-01, REQ-CNT-05 (UI): ConfirmDialog as a Reka AlertDialog (docs/contracts/ui.md §3.18).
// The dialog may be teleported to <body>, so elements are queried on document.
import { afterEach, describe, expect, it, vi } from 'vitest'
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

async function click(id: string) {
  const el = q(id)
  expect(el).not.toBeNull()
  el?.click()
  await nextTick()
}

async function pressEscape() {
  const target = (document.activeElement as HTMLElement | null) ?? document.body
  target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  await nextTick()
  await nextTick()
}

describe('ConfirmDialog (REQ-APR-01)', () => {
  it('renders nothing while closed', async () => {
    await mountDialog({ open: false })
    expect(q('confirm-dialog')).toBeNull()
  })

  it.each(['approve', 'send', 'regenerate'] as const)(
    'kind %s: alertdialog labelled by its title',
    async (kind) => {
      await mountDialog({ kind, counts: kind === 'send' ? { sms: 350, email: 368 } : null })
      const root = q('confirm-dialog')
      expect(root).not.toBeNull()
      expect(root?.getAttribute('data-kind')).toBe(kind)
      expect(root?.getAttribute('role')).toBe('alertdialog')
      expect(root?.getAttribute('aria-modal')).toBe('true')
      const title = q('confirm-dialog-title')
      expect(title?.textContent).toContain(t(`confirm.${kind}.title` as Parameters<typeof t>[0]))
      expect(root?.getAttribute('aria-labelledby')).toBe(title?.id)
      expect(title?.id).toBeTruthy()
    },
  )

  it('focuses the cancel button when it opens (safe default)', async () => {
    await mountDialog()
    await vi.waitFor(() => expect(document.activeElement).toBe(q('confirm-dialog-cancel')))
  })

  it('confirm emits confirm once per click', async () => {
    const w = await mountDialog()
    await click('confirm-dialog-confirm')
    expect(w.emitted('confirm')).toHaveLength(1)
    await click('confirm-dialog-confirm')
    expect(w.emitted('confirm')).toHaveLength(2)
  })

  it('cancel emits cancel', async () => {
    const w = await mountDialog()
    await click('confirm-dialog-cancel')
    expect(w.emitted('cancel')).toBeDefined()
  })

  it('Escape emits cancel and update:open(false)', async () => {
    const w = await mountDialog()
    await vi.waitFor(() => expect(document.activeElement).toBe(q('confirm-dialog-cancel')))
    await pressEscape()
    expect(w.emitted('cancel')).toBeDefined()
    const open = w.emitted('update:open') as unknown[][] | undefined
    expect(open?.[open.length - 1]?.[0]).toBe(false)
  })

  it('while submitting: confirm is aria-disabled and inert, Escape does not cancel', async () => {
    const w = await mountDialog({ submitting: true })
    expect(q('confirm-dialog-confirm')?.getAttribute('aria-disabled')).toBe('true')
    await click('confirm-dialog-confirm')
    expect(w.emitted('confirm')).toBeUndefined()
    await pressEscape()
    expect(w.emitted('cancel')).toBeUndefined()
    expect(q('confirm-dialog')).not.toBeNull()
  })

  it('send shows the per-channel counts as raw data-value', async () => {
    await mountDialog({ kind: 'send', counts: { sms: 350, email: 368 } })
    expect(q('confirm-dialog-count-sms')?.getAttribute('data-value')).toBe('350')
    expect(q('confirm-dialog-count-email')?.getAttribute('data-value')).toBe('368')
  })

  it('approve shows no send counts', async () => {
    await mountDialog({ kind: 'approve' })
    expect(q('confirm-dialog-count-sms')).toBeNull()
  })

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

  it('no error box without an error', async () => {
    await mountDialog()
    expect(q('confirm-dialog-error')).toBeNull()
  })
})
