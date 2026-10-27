// REQ-APR-01, REQ-CNT-03, REQ-UI-01 (UI): the preview shows the rendered content for one sample
// recipient as text; the email preview is shown in a sandboxed frame.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SmsPreview from '@/components/SmsPreview.vue'
import EmailPreview from '@/components/EmailPreview.vue'
import { t } from '@/i18n/index'
import { P, preview, sel } from '../helpers/p4'

function mountSms(props: Record<string, unknown>) {
  return mount(SmsPreview, { props: { preview: null, loading: false, error: null, ...props } })
}

function mountEmail(props: Record<string, unknown>) {
  return mount(EmailPreview, { props: { preview: null, loading: false, error: null, ...props } })
}

describe('SmsPreview (REQ-APR-01, REQ-CNT-03)', () => {
  it('shows the rendered SMS text', () => {
    const p = preview()
    const w = mountSms({ preview: p })
    expect(w.get(sel('sms-preview-text')).element.textContent).toBe(p.sms.text)
  })

  it('an empty first name shows the fallback recipient label', () => {
    const p = preview({
      customer: { id: 'c', first_name: '', last_name: 'Kaya', city: 'İstanbul' },
    } as never)
    expect(mountSms({ preview: p }).get(sel('sms-preview-recipient')).text()).toBe(
      t('preview.fallbackRecipient'),
    )
  })

  it('renders text, never HTML (REQ-UI-01)', () => {
    const p = preview()
    p.sms.text = P
    const w = mountSms({ preview: p })
    const el = w.get(sel('sms-preview-text')).element
    expect(el.textContent).toBe(P)
    expect(el.querySelector('script')).toBeNull()
    expect(el.querySelector('b')).toBeNull()
    expect(el.innerHTML).toContain('&lt;script&gt;')
  })
})

describe('EmailPreview (REQ-UI-01)', () => {
  it('shows the backend HTML in a fully sandboxed iframe, verbatim', () => {
    const p = preview()
    const w = mountEmail({ preview: p })
    expect(w.get(sel('email-preview-subject')).element.textContent).toBe(p.email.subject)
    const frame = w.get(sel('email-preview-frame')).element as HTMLIFrameElement
    expect(frame.tagName).toBe('IFRAME')
    expect(frame.getAttribute('sandbox')).toBe('')
    expect(frame.getAttribute('srcdoc')).toBe(p.email.html)
  })

  it('subject is text, never HTML (REQ-UI-01)', () => {
    const p = preview()
    p.email.subject = P
    const w = mountEmail({ preview: p })
    const el = w.get(sel('email-preview-subject')).element
    expect(el.textContent).toBe(P)
    expect(el.querySelector('script')).toBeNull()
    expect(el.querySelector('b')).toBeNull()
  })
})
