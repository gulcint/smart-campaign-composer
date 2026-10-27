// REQ-CNT-04, REQ-UI-01 (UI): StreamingText phases (docs/contracts/ui.md §3.10, §6.1).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import StreamingText from '@/components/StreamingText.vue'
import { P, fieldError, sel } from '../helpers/p4'

function mountText(props: Record<string, unknown>) {
  return mount(StreamingText, {
    props: { field: 'sms-body', text: '', phase: 'idle', errors: [], ...props },
  })
}

describe('StreamingText (REQ-CNT-04)', () => {
  it.each([
    {
      phase: 'idle',
      badge: 'draft',
      busy: false,
      caret: false,
      retrying: false,
      interrupted: false,
    },
    {
      phase: 'streaming',
      badge: 'draft',
      busy: true,
      caret: true,
      retrying: false,
      interrupted: false,
    },
    {
      phase: 'retrying',
      badge: 'draft',
      busy: true,
      caret: false,
      retrying: true,
      interrupted: false,
    },
    {
      phase: 'validated',
      badge: 'validated',
      busy: false,
      caret: false,
      retrying: false,
      interrupted: false,
    },
    {
      phase: 'invalid',
      badge: 'invalid',
      busy: false,
      caret: false,
      retrying: false,
      interrupted: false,
    },
    {
      phase: 'interrupted',
      badge: 'interrupted',
      busy: false,
      caret: false,
      retrying: false,
      interrupted: true,
    },
  ])('phase $phase', ({ phase, badge, busy, caret, retrying, interrupted }) => {
    const w = mountText({ phase, text: 'Merhaba' })
    const root = w.get(sel('streaming-text-sms-body'))
    expect(root.attributes('data-phase')).toBe(phase)
    expect(root.attributes('aria-busy') === 'true').toBe(busy)
    expect(w.get(sel('streaming-text-badge-sms-body')).attributes('data-status')).toBe(badge)
    expect(w.find(sel('streaming-text-caret-sms-body')).exists()).toBe(caret)
    expect(w.find(sel('streaming-text-retrying-sms-body')).exists()).toBe(retrying)
    expect(w.find(sel('streaming-text-interrupted-sms-body')).exists()).toBe(interrupted)
    expect(w.find(sel('streaming-text-regenerate-sms-body')).exists()).toBe(interrupted)
    expect(w.find(sel('streaming-text-errors-sms-body')).exists()).toBe(phase === 'invalid')
    expect(w.get(sel('streaming-text-content-sms-body')).element.textContent).toBe('Merhaba')
  })

  it('invalid lists one field-error per error with its code', () => {
    const w = mountText({
      field: 'email-subject',
      phase: 'invalid',
      errors: [
        fieldError('email.subject', 'subject_too_long', 80, 81),
        fieldError('', 'invalid_json'),
      ],
    })
    const codes = w
      .get(sel('streaming-text-errors-email-subject'))
      .findAll(sel('field-error'))
      .map((e) => e.attributes('data-code'))
    expect(codes).toEqual(['subject_too_long', 'invalid_json'])
  })

  it('interrupted offers regenerate', async () => {
    const w = mountText({ phase: 'interrupted', text: 'Merh' })
    await w.get(sel('streaming-text-regenerate-sms-body')).trigger('click')
    expect(w.emitted('regenerate')).toHaveLength(1)
  })

  it('renders streamed text as text, never HTML (REQ-UI-01)', () => {
    const w = mountText({ phase: 'streaming', text: P, field: 'email-body' })
    const el = w.get(sel('streaming-text-content-email-body')).element
    expect(el.textContent).toBe(P)
    expect(el.querySelector('script')).toBeNull()
    expect(el.querySelector('b')).toBeNull()
    expect(el.innerHTML).toContain('&lt;script&gt;')
  })
})
