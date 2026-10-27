// REQ-CNT-04, REQ-UI-01 (UI): streamed content is a draft until validated; invalid shows field
// errors; streamed text is never interpreted as HTML.
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
    { phase: 'streaming', badge: 'draft' },
    { phase: 'validated', badge: 'validated' },
    { phase: 'invalid', badge: 'invalid' },
  ])('phase $phase shows the $badge badge and the text', ({ phase, badge }) => {
    const w = mountText({ phase, text: 'Merhaba' })
    expect(w.get(sel('streaming-text-sms-body')).attributes('data-phase')).toBe(phase)
    expect(w.get(sel('streaming-text-badge-sms-body')).attributes('data-status')).toBe(badge)
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

  it('renders streamed text as text, never HTML (REQ-UI-01)', () => {
    const w = mountText({ phase: 'streaming', text: P, field: 'email-body' })
    const el = w.get(sel('streaming-text-content-email-body')).element
    expect(el.textContent).toBe(P)
    expect(el.querySelector('script')).toBeNull()
    expect(el.querySelector('b')).toBeNull()
    expect(el.innerHTML).toContain('&lt;script&gt;')
  })
})
