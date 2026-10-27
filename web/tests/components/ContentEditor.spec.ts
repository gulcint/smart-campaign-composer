// REQ-APR-01, REQ-APR-02, REQ-UI-01 (UI): the marketer can edit the SMS body and the email
// subject/body; a rejected edit shows its field error; content is shown as text, never as HTML.
import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import ContentEditor from '@/components/ContentEditor.vue'
import type { Content } from '@/api/types'
import { P, content, fieldError, sel } from '../helpers/p4'

let wrapper: VueWrapper | undefined

function mountEditor(props: Record<string, unknown> = {}) {
  wrapper = mount(ContentEditor, {
    props: {
      modelValue: content(),
      channel: 'sms',
      mode: 'editable',
      dirty: false,
      saving: false,
      fieldErrors: [],
      worstCase: null,
      conflict: null,
      ...props,
    },
    attachTo: document.body,
  })
  return wrapper
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

function lastModel(w: VueWrapper): Content {
  const ev = w.emitted('update:modelValue')
  expect(ev).toBeDefined()
  return (ev as unknown[][])[(ev as unknown[][]).length - 1]?.[0] as Content
}

describe('ContentEditor fields (REQ-APR-01)', () => {
  it('sms channel shows the SMS body for editing', () => {
    const w = mountEditor({ channel: 'sms' })
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      content().sms.body,
    )
  })

  it('email channel shows the subject and the body for editing', () => {
    const w = mountEditor({ channel: 'email' })
    expect((w.get(sel('editor-email-subject')).element as HTMLInputElement).value).toBe(
      content().email.subject,
    )
    expect((w.get(sel('editor-email-body')).element as HTMLTextAreaElement).value).toBe(
      content().email.body,
    )
  })

  it('typing emits the full content with only that field changed', async () => {
    const w = mountEditor({ channel: 'sms' })
    await w.get(sel('editor-sms-body')).setValue('Yeni SMS {{first_name}}')
    const c = lastModel(w)
    expect(c.sms.body).toBe('Yeni SMS {{first_name}}')
    expect(c.email).toEqual(content().email)
  })

  it('typing in the subject keeps the SMS in the emitted content', async () => {
    const w = mountEditor({ channel: 'email' })
    await w.get(sel('editor-email-subject')).setValue('Yeni konu')
    const c = lastModel(w)
    expect(c.email.subject).toBe('Yeni konu')
    expect(c.sms).toEqual(content().sms)
  })

  it('a dirty editable editor saves the full content (both channels)', async () => {
    const c = content('SMS metni', 'Konu', 'Gövde')
    const w = mountEditor({ channel: 'email', dirty: true, modelValue: c })
    await w.get(sel('editor-save')).trigger('click')
    expect(w.emitted('save')).toHaveLength(1)
    expect(w.emitted('save')?.[0]?.[0]).toEqual(c)
  })
})

describe('ContentEditor field errors (REQ-APR-02)', () => {
  it('a rejected edit shows the field error on its field', () => {
    const w = mountEditor({ fieldErrors: [fieldError('sms.body', 'sms_too_long', 160, 161)] })
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
  })
})

describe('ContentEditor renders text, never HTML (REQ-UI-01)', () => {
  it('sms body', () => {
    const w = mountEditor({ modelValue: content(P) })
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(P)
    expect(w.element.querySelector('script')).toBeNull()
    expect(w.element.querySelector('b')).toBeNull()
  })

  it('email subject and body', () => {
    const w = mountEditor({ channel: 'email', modelValue: content('x', P, P) })
    expect((w.get(sel('editor-email-subject')).element as HTMLInputElement).value).toBe(P)
    expect((w.get(sel('editor-email-body')).element as HTMLTextAreaElement).value).toBe(P)
    expect(w.element.querySelector('script')).toBeNull()
    expect(w.element.querySelector('b')).toBeNull()
  })
})
