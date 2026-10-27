// REQ-APR-01, REQ-APR-02, REQ-APR-03, REQ-CNT-01, REQ-CNT-03, REQ-UI-01 (UI): ContentEditor
// (docs/contracts/ui.md §3.14, §6.1; DECISIONS D40).
import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import ContentEditor from '@/components/ContentEditor.vue'
import { t } from '@/i18n/index'
import type { Content, FieldError } from '@/api/types'
import { P, V2, content, fieldError, sel } from '../helpers/p4'

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

function describedIds(el: Element): string[] {
  return (el.getAttribute('aria-describedby') ?? '').split(/\s+/).filter(Boolean)
}

describe('ContentEditor fields (REQ-APR-01)', () => {
  it('sms channel shows only the SMS body and its counter', () => {
    const w = mountEditor({ channel: 'sms' })
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      content().sms.body,
    )
    expect(w.find(sel('char-counter-sms-body')).exists()).toBe(true)
    expect(w.find(sel('editor-email-subject')).exists()).toBe(false)
    expect(w.find(sel('editor-email-body')).exists()).toBe(false)
  })

  it('email channel shows subject (input), body (textarea) and the subject counter', () => {
    const w = mountEditor({ channel: 'email' })
    const subject = w.get(sel('editor-email-subject')).element
    expect(subject.tagName).toBe('INPUT')
    expect((subject as HTMLInputElement).value).toBe(content().email.subject)
    const body = w.get(sel('editor-email-body')).element
    expect(body.tagName).toBe('TEXTAREA')
    expect((body as HTMLTextAreaElement).value).toBe(content().email.body)
    expect(w.find(sel('char-counter-email-subject')).exists()).toBe(true)
    expect(w.find(sel('editor-sms-body')).exists()).toBe(false)
  })

  it('typing emits the FULL content with only that field changed', async () => {
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
    expect(c.email.body).toBe(content().email.body)
  })

  it('shows the worst-case second counter line from worstCase', () => {
    const w = mountEditor({ worstCase: { sms_runes: 158, subject_runes: 42 } })
    expect(w.get(sel('char-counter-worst-sms-body')).attributes('data-count')).toBe('158')
  })
})

describe('ContentEditor save (REQ-APR-01, REQ-APR-02)', () => {
  it.each([
    { name: 'not dirty', props: { dirty: false } },
    { name: 'saving', props: { dirty: true, saving: true } },
    { name: 'readonly', props: { dirty: true, mode: 'readonly' } },
    { name: 'locked', props: { dirty: true, mode: 'locked' } },
  ])('editor-save is aria-disabled and emits nothing when $name', async ({ props }) => {
    const w = mountEditor(props)
    const save = w.get(sel('editor-save'))
    expect(save.attributes('aria-disabled')).toBe('true')
    await save.trigger('click')
    await w.get(sel('editor-sms-body')).trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(w.emitted('save')).toBeUndefined()
  })

  it('a dirty editable editor saves the full content (both channels) from the email tab', async () => {
    const c = content('SMS metni', 'Konu', 'Gövde')
    const w = mountEditor({ channel: 'email', dirty: true, modelValue: c })
    const save = w.get(sel('editor-save'))
    expect(save.attributes('aria-disabled')).not.toBe('true')
    await save.trigger('click')
    expect(w.emitted('save')).toHaveLength(1)
    expect(w.emitted('save')?.[0]?.[0]).toEqual(c)
  })

  it('Ctrl+Enter and Cmd+Enter in a field save; plain Enter does not', async () => {
    const w = mountEditor({ dirty: true })
    const area = w.get(sel('editor-sms-body'))
    await area.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('save')).toBeUndefined()
    await area.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(w.emitted('save')).toHaveLength(1)
    await area.trigger('keydown', { key: 'Enter', metaKey: true })
    expect(w.emitted('save')).toHaveLength(2)
  })

  it('Ctrl+Enter in the subject input saves too', async () => {
    const w = mountEditor({ channel: 'email', dirty: true })
    await w.get(sel('editor-email-subject')).trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(w.emitted('save')).toHaveLength(1)
  })
})

describe('ContentEditor modes (REQ-APR-01)', () => {
  it('saving makes the fields readonly', () => {
    const w = mountEditor({ dirty: true, saving: true })
    expect(w.get(sel('editor-sms-body')).attributes('readonly')).toBeDefined()
  })

  it.each(['readonly', 'locked'])(
    'mode %s makes the fields readonly and disables the chips',
    (mode) => {
      const w = mountEditor({ mode, channel: 'email' })
      expect(w.get(sel('editor-email-subject')).attributes('readonly')).toBeDefined()
      expect(w.get(sel('editor-email-body')).attributes('readonly')).toBeDefined()
      for (const v of ['first_name', 'last_name', 'city']) {
        const chip = w.get(sel(`variable-chip-${v}`))
        const disabled =
          chip.attributes('aria-disabled') === 'true' || chip.attributes('disabled') !== undefined
        expect(disabled).toBe(true)
      }
    },
  )

  it('locked shows the locked note; editable does not', () => {
    expect(mountEditor({ mode: 'locked' }).find(sel('editor-locked-note')).exists()).toBe(true)
    wrapper?.unmount()
    expect(mountEditor({ mode: 'editable' }).find(sel('editor-locked-note')).exists()).toBe(false)
  })

  it('editable fields are not readonly', () => {
    const w = mountEditor()
    expect(w.get(sel('editor-sms-body')).attributes('readonly')).toBeUndefined()
  })
})

describe('ContentEditor variable chips (REQ-CNT-03)', () => {
  it('offers insert chips for exactly first_name, last_name and city', () => {
    const w = mountEditor()
    for (const v of ['first_name', 'last_name', 'city']) {
      const chip = w.get(sel(`variable-chip-${v}`))
      expect(chip.attributes('data-variant')).toBe('insert')
      expect(chip.text()).toContain(`{{${v}}}`)
    }
    expect(w.find(sel('variable-chip-password')).exists()).toBe(false)
  })

  it('replaces the selection of the last focused field', async () => {
    const w = mountEditor({
      channel: 'email',
      modelValue: content('SMS', 'Sepetin seni bekliyor', 'Gövde'),
    })
    const input = w.get(sel('editor-email-subject'))
    const el = input.element as HTMLInputElement
    el.focus()
    await input.trigger('focus')
    el.setSelectionRange(0, 7) // "Sepetin"
    await input.trigger('select')
    await w.get(sel('variable-chip-first_name')).trigger('click')
    const c = lastModel(w)
    expect(c.email.subject).toBe('{{first_name}} seni bekliyor')
    expect(c.email.body).toBe('Gövde')
    expect(c.sms.body).toBe('SMS')
  })

  it('without a focused field inserts into the channel body field', async () => {
    const w = mountEditor({ channel: 'email', modelValue: content('SMS', 'Konu', 'Gövde') })
    await w.get(sel('variable-chip-city')).trigger('click')
    const c = lastModel(w)
    expect(c.email.body).toContain('{{city}}')
    expect(c.email.subject).toBe('Konu')
    expect(c.sms.body).toBe('SMS')
  })
})

describe('ContentEditor field errors (REQ-APR-02, REQ-CNT-01)', () => {
  it('a field error shows inline-error-<f>, marks the field invalid and is described by it', () => {
    const w = mountEditor({ fieldErrors: [fieldError('sms.body', 'sms_too_long', 160, 161)] })
    const err = w.get(sel('inline-error-sms-body'))
    expect(err.attributes('data-code')).toBe('sms_too_long')
    const area = w.get(sel('editor-sms-body'))
    expect(area.attributes('aria-invalid')).toBe('true')
    const ids = describedIds(area.element)
    const targets = ids.map((id) => document.getElementById(id)?.getAttribute('data-testid'))
    expect(targets).toContain('inline-error-sms-body')
    expect(targets).toContain('char-counter-sms-body')
  })

  it('an over-limit counter marks the field invalid without a field error', () => {
    const w = mountEditor({ modelValue: content('a'.repeat(161)) })
    expect(w.get(sel('char-counter-sms-body')).attributes('data-state')).toBe('over-limit')
    expect(w.get(sel('editor-sms-body')).attributes('aria-invalid')).toBe('true')
  })

  it('a valid field is not marked invalid', () => {
    const w = mountEditor({ modelValue: content('a'.repeat(160)) })
    expect(w.get(sel('editor-sms-body')).attributes('aria-invalid')).not.toBe('true')
  })

  it('path "" errors are shown on every visible field', () => {
    const w = mountEditor({ channel: 'email', fieldErrors: [fieldError('', 'internal_error')] })
    expect(w.get(sel('inline-error-email-subject')).attributes('data-code')).toBe('internal_error')
    expect(w.get(sel('inline-error-email-body')).attributes('data-code')).toBe('internal_error')
  })

  it('errors of other fields are routed to their own field', () => {
    const w = mountEditor({
      channel: 'email',
      fieldErrors: [
        fieldError('email.body', 'malformed_variable'),
        fieldError('sms.body', 'unknown_variable'),
      ],
    })
    expect(w.get(sel('inline-error-email-body')).attributes('data-code')).toBe('malformed_variable')
    expect(w.find(sel('inline-error-email-subject')).exists()).toBe(false)
  })
})

// ui.md §3.14 (e): path "" errors are added to every field's inline-error-<f>; §4: the 413
// payload_too_large pseudo-error is shown on the editor field with data-code="payload_too_large".
// Whatever the number of inline-error elements, element ids stay unique in the document and every
// id in a field's aria-describedby resolves to exactly one element.
describe('ContentEditor inline error ids (REQ-APR-02, ui.md §3.14 e, §4)', () => {
  function assertUniqueIds() {
    const ids = [...document.querySelectorAll('[id]')].map((el) => el.id)
    const dupes = ids.filter((id, i) => ids.indexOf(id) !== i)
    expect(dupes).toEqual([])
  }

  function assertDescribedBy(field: string) {
    const el = document.querySelector(sel(`editor-${field}`)) as HTMLElement
    expect(el).not.toBeNull()
    const ids = describedIds(el)
    expect(ids.length).toBeGreaterThan(0)
    for (const id of ids) {
      expect([...document.querySelectorAll('[id]')].filter((x) => x.id === id)).toHaveLength(1)
    }
    const targets = ids.map((id) => document.getElementById(id)?.getAttribute('data-testid'))
    expect(targets).toContain(`inline-error-${field}`)
    expect(el.getAttribute('aria-invalid')).toBe('true')
  }

  interface IdCase {
    name: string
    channel: 'sms' | 'email'
    errors: FieldError[]
    fields: string[]
    codes: Record<string, string[]>
  }

  const idCases: IdCase[] = [
    {
      name: 'path "" plus an error of the SMS body',
      channel: 'sms',
      errors: [fieldError('', 'internal_error'), fieldError('sms.body', 'sms_too_long', 160, 161)],
      fields: ['sms-body'],
      codes: { 'sms-body': ['sms_too_long'] },
    },
    {
      name: 'payload_too_large on the SMS body plus a path "" error',
      channel: 'sms',
      errors: [fieldError('sms.body', 'payload_too_large'), fieldError('', 'internal_error')],
      fields: ['sms-body'],
      codes: { 'sms-body': ['payload_too_large'] },
    },
    {
      name: 'path "" plus errors on both email fields',
      channel: 'email',
      errors: [
        fieldError('', 'internal_error'),
        fieldError('email.subject', 'subject_too_long', 80, 81),
        fieldError('email.body', 'payload_too_large'),
      ],
      fields: ['email-subject', 'email-body'],
      codes: { 'email-subject': ['subject_too_long'], 'email-body': ['payload_too_large'] },
    },
  ]

  it.each(idCases)(
    '$name: unique ids, aria-describedby resolves to one element each',
    ({ channel, errors, fields, codes }) => {
      mountEditor({ channel, fieldErrors: errors, dirty: true })
      assertUniqueIds()
      for (const f of fields) {
        assertDescribedBy(f)
        const shown = [...document.querySelectorAll(sel(`inline-error-${f}`))].map((el) =>
          el.getAttribute('data-code'),
        )
        for (const code of codes[f] ?? []) {
          expect(shown).toContain(code)
        }
      }
    },
  )
})

describe('ContentEditor conflict (REQ-APR-03, D40)', () => {
  const conflict = { currentVersionId: V2, currentVersionNumber: 4 }

  it('no conflict panel without a conflict', () => {
    expect(mountEditor().find(sel('editor-conflict')).exists()).toBe(false)
  })

  it('shows editor-conflict as an alert with the current version', () => {
    const w = mountEditor({ conflict, dirty: true })
    const panel = w.get(sel('editor-conflict'))
    expect(panel.attributes('role')).toBe('alert')
    expect(panel.attributes('data-current-version-id')).toBe(V2)
    expect(panel.attributes('data-value')).toBe('4')
    expect(panel.text()).toContain(t('editor.conflict.title', { n: 4 }))
    expect(panel.find(sel('editor-conflict-reload')).text()).toContain(t('editor.conflict.reload'))
    expect(panel.find(sel('editor-conflict-keep-mine')).text()).toContain(
      t('editor.conflict.keepMine'),
    )
  })

  it('a conflict without a current version leaves the attributes empty', () => {
    const w = mountEditor({ conflict: { currentVersionId: null, currentVersionNumber: null } })
    const panel = w.get(sel('editor-conflict'))
    expect(panel.attributes('data-current-version-id') ?? '').toBe('')
    expect(panel.attributes('data-value') ?? '').toBe('')
  })

  it('always shows the keep-mine warning inside the panel, and keep-mine is described by it', () => {
    const w = mountEditor({ conflict, dirty: true })
    const panel = w.get(sel('editor-conflict'))
    const warning = panel.get(sel('editor-conflict-warning'))
    expect(warning.text()).toContain(t('editor.conflict.keepMineWarning'))
    const id = warning.attributes('id')
    expect(id).toBeTruthy()
    const keep = panel.get(sel('editor-conflict-keep-mine'))
    expect(describedIds(keep.element)).toContain(id)
  })

  it('reload emits reload-latest; keep-mine emits keep-mine (no confirmation dialog)', async () => {
    const w = mountEditor({ conflict, dirty: true })
    await w.get(sel('editor-conflict-reload')).trigger('click')
    expect(w.emitted('reload-latest')).toHaveLength(1)
    await w.get(sel('editor-conflict-keep-mine')).trigger('click')
    expect(w.emitted('keep-mine')).toHaveLength(1)
    expect(document.querySelector(sel('confirm-dialog'))).toBeNull()
  })

  it('save is disabled while the conflict is shown, but the text stays editable', async () => {
    const w = mountEditor({ conflict, dirty: true, modelValue: content('Benim metnim') })
    const save = w.get(sel('editor-save'))
    expect(save.attributes('aria-disabled')).toBe('true')
    await save.trigger('click')
    await w.get(sel('editor-sms-body')).trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(w.emitted('save')).toBeUndefined()
    const area = w.get(sel('editor-sms-body'))
    expect(area.attributes('readonly')).toBeUndefined()
    expect((area.element as HTMLTextAreaElement).value).toBe('Benim metnim')
  })

  it('focus moves to editor-conflict-reload when the conflict opens', async () => {
    const w = mountEditor({ dirty: true })
    await w.setProps({ conflict })
    await nextTick()
    await nextTick()
    expect(document.activeElement).toBe(w.get(sel('editor-conflict-reload')).element)
  })
})

describe('ContentEditor renders text, never HTML (REQ-UI-01, ui.md §6.1)', () => {
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
