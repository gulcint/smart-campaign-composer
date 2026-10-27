// REQ-APR-01, REQ-CNT-03, REQ-UI-01 (UI): SmsPreview and EmailPreview (docs/contracts/ui.md
// §3.15, §3.16, §6.1, §6.3).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SmsPreview from '@/components/SmsPreview.vue'
import EmailPreview from '@/components/EmailPreview.vue'
import { t } from '@/i18n/index'
import { P, apiError, preview, sel } from '../helpers/p4'

function mountSms(props: Record<string, unknown>) {
  return mount(SmsPreview, { props: { preview: null, loading: false, error: null, ...props } })
}

function mountEmail(props: Record<string, unknown>) {
  return mount(EmailPreview, { props: { preview: null, loading: false, error: null, ...props } })
}

describe('SmsPreview (REQ-APR-01, REQ-CNT-03)', () => {
  it.each([
    { name: 'empty', props: {}, state: 'empty' },
    { name: 'loading', props: { loading: true }, state: 'loading' },
    {
      name: 'loading wins over error',
      props: { loading: true, error: apiError(500, 'internal_error') },
      state: 'loading',
    },
    {
      name: 'error wins over preview',
      props: { error: apiError(500, 'internal_error'), preview: preview() },
      state: 'error',
    },
    { name: 'ready', props: { preview: preview() }, state: 'ready' },
  ])('data-state $state when $name', ({ props, state }) => {
    expect(mountSms(props).get(sel('sms-preview')).attributes('data-state')).toBe(state)
  })

  it('shows the rendered SMS text exactly', () => {
    const p = preview()
    const w = mountSms({ preview: p })
    expect(w.get(sel('sms-preview-text')).element.textContent).toBe(p.sms.text)
    expect(w.get(sel('sms-preview-recipient')).text()).toBe('Ayşe')
  })

  // ui.md §3.15: sms-preview-text textContent is EXACTLY preview.sms.text — the template adds no
  // leading/trailing whitespace or line breaks around it and does not trim or collapse the text.
  it.each([
    { name: 'plain text', text: 'Merhaba Ayşe, sepetindeki ürünler seni bekliyor!' },
    { name: 'leading and trailing spaces kept', text: '  Merhaba Ayşe  ' },
    {
      name: 'inner line break and double space kept',
      text: 'Merhaba Ayşe,\nindirim  seni bekliyor',
    },
    { name: 'single character', text: 'x' },
  ])('sms-preview-text textContent equals sms.text exactly ($name)', ({ text }) => {
    const p = preview()
    p.sms.text = text
    const el = mountSms({ preview: p }).get(sel('sms-preview-text')).element
    expect(el.textContent).toBe(text)
    expect(el.textContent?.length).toBe(text.length)
  })

  it('an empty first name shows the fallback recipient label', () => {
    const p = preview({
      customer: { id: 'c', first_name: '', last_name: 'Kaya', city: 'İstanbul' },
    } as never)
    expect(mountSms({ preview: p }).get(sel('sms-preview-recipient')).text()).toBe(
      t('preview.fallbackRecipient'),
    )
  })

  // D49: the UI walks only the first min(sample_count, 5) samples.
  it.each([
    { index: 4, count: 584, i: 5, n: 5 },
    { index: 0, count: 584, i: 1, n: 5 },
    { index: 0, count: 5, i: 1, n: 5 },
    { index: 2, count: 3, i: 3, n: 3 },
    { index: 0, count: 1, i: 1, n: 1 },
  ])(
    'shows the sample position 1-based over min(sample_count, 5): index $index of $count => $i/$n',
    ({ index, count, i, n }) => {
      const w = mountSms({ preview: preview({ sample_index: index, sample_count: count }) })
      expect(w.get(sel('sms-preview-sample')).text()).toBe(t('preview.sample', { i, n }))
    },
  )

  it('next-sample emits; disabled when there is at most one sample', async () => {
    const w = mountSms({ preview: preview() })
    await w.get(sel('sms-preview-next')).trigger('click')
    expect(w.emitted('next-sample')).toHaveLength(1)
    const one = mountSms({ preview: preview({ sample_count: 1 }) })
    expect(one.get(sel('sms-preview-next')).attributes('aria-disabled')).toBe('true')
    await one.get(sel('sms-preview-next')).trigger('click')
    expect(one.emitted('next-sample')).toBeUndefined()
  })

  it.each([
    { name: 'empty', props: {} },
    { name: 'error', props: { error: apiError(500, 'internal_error'), preview: preview() } },
  ])('no text element when $name', ({ props }) => {
    expect(mountSms(props).find(sel('sms-preview-text')).exists()).toBe(false)
  })

  it('customer_not_found keeps next-sample usable', async () => {
    const w = mountSms({ preview: preview(), error: apiError(404, 'customer_not_found') })
    const next = w.get(sel('sms-preview-next'))
    expect(next.attributes('aria-disabled')).not.toBe('true')
    await next.trigger('click')
    expect(w.emitted('next-sample')).toHaveLength(1)
  })

  it('loading keeps the previous text in place', () => {
    const p = preview()
    const w = mountSms({ preview: p, loading: true })
    expect(w.get(sel('sms-preview-text')).element.textContent).toBe(p.sms.text)
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

describe('EmailPreview (REQ-APR-01, REQ-UI-01)', () => {
  it('shows the subject and the backend HTML in a fully sandboxed iframe, verbatim', () => {
    const p = preview()
    const w = mountEmail({ preview: p })
    expect(w.get(sel('email-preview')).attributes('data-state')).toBe('ready')
    expect(w.get(sel('email-preview-subject')).element.textContent).toBe(p.email.subject)
    const frame = w.get(sel('email-preview-frame')).element as HTMLIFrameElement
    expect(frame.tagName).toBe('IFRAME')
    expect(frame.getAttribute('sandbox')).toBe('')
    expect(frame.getAttribute('srcdoc')).toBe(p.email.html)
    expect(frame.getAttribute('title')).toBe(t('preview.emailFrameTitle'))
    expect(frame.hasAttribute('src')).toBe(false)
  })

  it.each([
    { name: 'empty', props: {}, state: 'empty' },
    { name: 'loading', props: { loading: true }, state: 'loading' },
    { name: 'error', props: { error: apiError(409, 'invalid_state') }, state: 'error' },
  ])('data-state $state and no frame when $name', ({ props, state }) => {
    const w = mountEmail(props)
    expect(w.get(sel('email-preview')).attributes('data-state')).toBe(state)
    if (state !== 'loading') expect(w.find(sel('email-preview-frame')).exists()).toBe(false)
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
