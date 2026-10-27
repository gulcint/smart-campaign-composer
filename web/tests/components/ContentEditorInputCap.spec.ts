// REQ-CNT-01, REQ-CNT-02, REQ-CNT-05, REQ-APR-02 (UI): ContentEditor input cap
// (docs/contracts/ui.md §3.14 d2 and §3.13, owner decision D48).
//
// Inputs are simulated the way a browser delivers them, so the test does not depend on which event
// the component listens to: for a paste a cancelable `paste` event (clipboardData stub) comes first;
// then a cancelable `beforeinput` (inputType, data); if neither was cancelled, the field value is
// changed as the browser would and an `input` event follows. The editor is mounted with a v-model
// loop (update:modelValue -> setProps) so the counter follows the field like in the page.
import { afterEach, describe, expect, it } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import ContentEditor from '@/components/ContentEditor.vue'
import type { Content } from '@/api/types'
import { content, sel } from '../helpers/p4'

type Field = 'sms-body' | 'email-subject' | 'email-body'

let wrapper: VueWrapper | undefined

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

const runes = (s: string) => [...s.normalize('NFC')].length

function mountEditor(props: Record<string, unknown> = {}) {
  const w: VueWrapper = mount(ContentEditor, {
    props: {
      modelValue: content(),
      channel: 'sms',
      mode: 'editable',
      dirty: true,
      saving: false,
      fieldErrors: [],
      worstCase: null,
      conflict: null,
      ...props,
      'onUpdate:modelValue': (c: Content) => w.setProps({ modelValue: c }),
    },
    attachTo: document.body,
  })
  wrapper = w
  return w
}

function fieldEl(w: VueWrapper, f: Field) {
  return w.get(sel(`editor-${f}`)).element as HTMLInputElement | HTMLTextAreaElement
}

function modelText(w: VueWrapper, f: Field): string {
  const c = (w.props() as Record<string, unknown>).modelValue as Content
  if (f === 'sms-body') return c.sms.body
  if (f === 'email-subject') return c.email.subject
  return c.email.body
}

function emits(w: VueWrapper): Content[] {
  return ((w.emitted('update:modelValue') ?? []) as unknown[][]).map((e) => e[0] as Content)
}

function clipboard(text: string) {
  return { getData: (type: string) => (type.startsWith('text') ? text : ''), types: ['text/plain'] }
}

/**
 * Replaces [start, end) (default: caret at the end) with data, or deletes the range for delete*
 * input types, honouring preventDefault on paste/beforeinput.
 */
async function simulateInput(
  el: HTMLInputElement | HTMLTextAreaElement,
  inputType: string,
  data: string | null,
  start = el.value.length,
  end = start,
) {
  el.focus()
  el.setSelectionRange(start, end)
  if (inputType === 'insertFromPaste') {
    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', { value: clipboard(data ?? '') })
    el.dispatchEvent(paste)
    if (paste.defaultPrevented) {
      await flushPromises()
      return
    }
  }
  const before = new InputEvent('beforeinput', { bubbles: true, cancelable: true, inputType, data })
  if (inputType === 'insertFromPaste') {
    Object.defineProperty(before, 'dataTransfer', { value: clipboard(data ?? '') })
  }
  el.dispatchEvent(before)
  if (!before.defaultPrevented) {
    const v = el.value
    const ins = inputType.startsWith('delete') ? '' : (data ?? '')
    el.value = v.slice(0, start) + ins + v.slice(end)
    const caret = start + ins.length
    el.setSelectionRange(caret, caret)
    el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType, data }))
  }
  await flushPromises()
}

const typeText = (el: HTMLInputElement | HTMLTextAreaElement, s: string) =>
  simulateInput(el, 'insertText', s)
const paste = (
  el: HTMLInputElement | HTMLTextAreaElement,
  s: string,
  start?: number,
  end?: number,
) => simulateInput(el, 'insertFromPaste', s, start, end)

function assertCounter(
  w: VueWrapper,
  f: 'sms-body' | 'email-subject',
  n: number,
  limit: number,
  state: string,
) {
  const value = w.get(sel(`char-counter-value-${f}`))
  expect(value.attributes('data-count')).toBe(String(n))
  expect(value.text()).toContain(`${n}/${limit}`)
  expect(w.get(sel(`char-counter-${f}`)).attributes('data-state')).toBe(state)
  const over = w.find(sel(`char-counter-over-${f}`))
  expect(over.exists()).toBe(state === 'over-limit')
  if (state === 'over-limit') expect(over.text()).toContain(`−${n - limit}`)
}

/** A refused input leaves the model unchanged (no emit, or an emit of the same text) and the field shows the model. */
function assertUnchanged(w: VueWrapper, f: Field, before: string, emitsBefore: number) {
  const after = emits(w).slice(emitsBefore)
  for (const c of after) {
    const t = f === 'sms-body' ? c.sms.body : f === 'email-subject' ? c.email.subject : c.email.body
    expect(t).toBe(before)
  }
  expect(modelText(w, f)).toBe(before)
  expect(fieldEl(w, f).value).toBe(before)
}

const cases = [
  { f: 'sms-body' as const, channel: 'sms', limit: 160, make: (s: string) => content(s) },
  {
    f: 'email-subject' as const,
    channel: 'email',
    limit: 80,
    make: (s: string) => content(undefined, s),
  },
]

describe.each(cases)(
  '$f input cap at $limit runes (ui.md §3.14 d2)',
  ({ f, channel, limit, make }) => {
    it('has no native maxlength (it would count UTF-16 units)', () => {
      const w = mountEditor({ channel, modelValue: make('a') })
      expect(fieldEl(w, f).hasAttribute('maxlength')).toBe(false)
    })

    it('typing at n == limit is refused; the counter stays limit/limit near-limit with no over line', async () => {
      const full = 'ş'.repeat(limit)
      const w = mountEditor({ channel, modelValue: make(full) })
      assertCounter(w, f, limit, limit, 'near-limit')
      await typeText(fieldEl(w, f), 'x')
      assertUnchanged(w, f, full, 0)
      assertCounter(w, f, limit, limit, 'near-limit')
    })

    it('typing up to the limit is applied; the next character is refused', async () => {
      const w = mountEditor({ channel, modelValue: make('a'.repeat(limit - 1)) })
      await typeText(fieldEl(w, f), 'b')
      expect(modelText(w, f)).toBe('a'.repeat(limit - 1) + 'b')
      assertCounter(w, f, limit, limit, 'near-limit')
      const n = emits(w).length
      await typeText(fieldEl(w, f), 'c')
      assertUnchanged(w, f, 'a'.repeat(limit - 1) + 'b', n)
    })

    it('a paste that does not fit inserts exactly the prefix that fits', async () => {
      const base = 'a'.repeat(limit - 20)
      const pasted = 'ğüşİıöçxyz'.repeat(5) // 50 runes
      const w = mountEditor({ channel, modelValue: make(base) })
      await paste(fieldEl(w, f), pasted)
      expect(modelText(w, f)).toBe(base + [...pasted].slice(0, 20).join(''))
      expect(fieldEl(w, f).value).toBe(modelText(w, f))
      assertCounter(w, f, limit, limit, 'near-limit')
    })

    it('a paste in the middle keeps the text after the caret and fills only up to the limit', async () => {
      const base = 'a'.repeat(limit - 5) // 5 runes free
      const w = mountEditor({ channel, modelValue: make(base) })
      await paste(fieldEl(w, f), '1234567890', 10, 10)
      expect(modelText(w, f)).toBe('a'.repeat(10) + '12345' + 'a'.repeat(limit - 15))
      assertCounter(w, f, limit, limit, 'near-limit')
    })

    it('counts runes, not UTF-16 units: an astral emoji counts 1 and is accepted up to the limit', async () => {
      const w = mountEditor({ channel, modelValue: make('a'.repeat(limit - 1)) })
      await typeText(fieldEl(w, f), '😀')
      expect(modelText(w, f)).toBe('a'.repeat(limit - 1) + '😀')
      expect(modelText(w, f).length).toBe(limit + 1) // UTF-16 units
      assertCounter(w, f, limit, limit, 'near-limit')
      const n = emits(w).length
      await typeText(fieldEl(w, f), '😀')
      assertUnchanged(w, f, 'a'.repeat(limit - 1) + '😀', n)
    })

    it('counts NFC: a decomposed letter (e + U+0301) counts 1 and is accepted at limit - 1', async () => {
      const w = mountEditor({ channel, modelValue: make('a'.repeat(limit - 1)) })
      await typeText(fieldEl(w, f), 'é')
      const got = modelText(w, f)
      expect(got.normalize('NFC')).toBe('a'.repeat(limit - 1) + 'é')
      expect(runes(got)).toBe(limit)
    })

    it('a pasted run of emoji is cut on rune boundaries (never half a surrogate pair)', async () => {
      const w = mountEditor({ channel, modelValue: make('a'.repeat(limit - 3)) })
      await paste(fieldEl(w, f), '😀😀😀😀😀')
      expect(modelText(w, f)).toBe('a'.repeat(limit - 3) + '😀😀😀')
      expect(fieldEl(w, f).value).toBe(modelText(w, f))
    })
  },
)

describe('chip insertion respects the cap (ui.md §3.14 d, d2)', () => {
  it('a chip that does not fit is not inserted and nothing is emitted', async () => {
    const base = 'a'.repeat(150) // {{first_name}} is 14 runes: 164 > 160
    const w = mountEditor({ modelValue: content(base) })
    const el = fieldEl(w, 'sms-body')
    el.focus()
    await w.get(sel('editor-sms-body')).trigger('focus')
    el.setSelectionRange(150, 150)
    await w.get(sel('editor-sms-body')).trigger('select')
    await w.get(sel('variable-chip-first_name')).trigger('click')
    await flushPromises()
    expect(w.emitted('update:modelValue')).toBeUndefined()
    expect(modelText(w, 'sms-body')).toBe(base)
    expect(el.value).toBe(base)
  })

  it('a chip that exactly fits is inserted (160/160)', async () => {
    const base = 'a'.repeat(146)
    const w = mountEditor({ modelValue: content(base) })
    const el = fieldEl(w, 'sms-body')
    el.focus()
    await w.get(sel('editor-sms-body')).trigger('focus')
    el.setSelectionRange(146, 146)
    await w.get(sel('editor-sms-body')).trigger('select')
    await w.get(sel('variable-chip-first_name')).trigger('click')
    await flushPromises()
    expect(modelText(w, 'sms-body')).toBe(base + '{{first_name}}')
    assertCounter(w, 'sms-body', 160, 160, 'near-limit')
  })

  it('a chip into the email subject that does not fit is not inserted', async () => {
    const base = 'b'.repeat(70)
    const w = mountEditor({ channel: 'email', modelValue: content(undefined, base) })
    const el = fieldEl(w, 'email-subject')
    el.focus()
    await w.get(sel('editor-email-subject')).trigger('focus')
    el.setSelectionRange(70, 70)
    await w.get(sel('editor-email-subject')).trigger('select')
    await w.get(sel('variable-chip-city')).trigger('click') // {{city}} = 8 runes: 78 fits
    await flushPromises()
    expect(modelText(w, 'email-subject')).toBe(base + '{{city}}')
    const n = emits(w).length
    await w.get(sel('variable-chip-first_name')).trigger('click') // 78 + 14 > 80
    await flushPromises()
    expect(emits(w)).toHaveLength(n)
    expect(modelText(w, 'email-subject')).toBe(base + '{{city}}')
  })
})

describe('text already over the limit (invalid draft) (ui.md §3.14 d2)', () => {
  const draft = 'x'.repeat(406)

  it('loads as is and shows the real count 406/160 over-limit with −246', () => {
    const w = mountEditor({ modelValue: content(draft) })
    expect(fieldEl(w, 'sms-body').value).toBe(draft)
    assertCounter(w, 'sms-body', 406, 160, 'over-limit')
    expect(w.get(sel('char-counter-over-sms-body')).text()).toContain('−246')
  })

  it('deleting is applied', async () => {
    const w = mountEditor({ modelValue: content(draft) })
    await simulateInput(fieldEl(w, 'sms-body'), 'deleteContentBackward', null, 400, 406)
    expect(modelText(w, 'sms-body')).toBe('x'.repeat(400))
    assertCounter(w, 'sms-body', 400, 160, 'over-limit')
  })

  it('typing at the end (would increase n) is refused; n stays 406', async () => {
    const w = mountEditor({ modelValue: content(draft) })
    await typeText(fieldEl(w, 'sms-body'), 'y')
    assertUnchanged(w, 'sms-body', draft, 0)
    assertCounter(w, 'sms-body', 406, 160, 'over-limit')
  })

  it('replacing a selection with text of equal length is applied', async () => {
    const w = mountEditor({ modelValue: content(draft) })
    await simulateInput(fieldEl(w, 'sms-body'), 'insertText', 'abc', 0, 3)
    expect(modelText(w, 'sms-body')).toBe('abc' + 'x'.repeat(403))
    assertCounter(w, 'sms-body', 406, 160, 'over-limit')
  })

  it('replacing a selection with shorter text is applied', async () => {
    const w = mountEditor({ modelValue: content(draft) })
    await simulateInput(fieldEl(w, 'sms-body'), 'insertText', 'k', 0, 5)
    expect(modelText(w, 'sms-body')).toBe('k' + 'x'.repeat(401))
    assertCounter(w, 'sms-body', 402, 160, 'over-limit')
  })

  it('a paste over a selection that would increase n is trimmed to no increase', async () => {
    const w = mountEditor({ modelValue: content(draft) })
    await paste(fieldEl(w, 'sms-body'), 'ABCDE', 0, 2)
    expect(modelText(w, 'sms-body')).toBe('AB' + 'x'.repeat(404))
    assertCounter(w, 'sms-body', 406, 160, 'over-limit')
  })

  it('an over-limit subject (90/80) follows the same rules', async () => {
    const subj = 's'.repeat(90)
    const w = mountEditor({ channel: 'email', modelValue: content(undefined, subj) })
    assertCounter(w, 'email-subject', 90, 80, 'over-limit')
    await typeText(fieldEl(w, 'email-subject'), 'y')
    assertUnchanged(w, 'email-subject', subj, 0)
    await simulateInput(fieldEl(w, 'email-subject'), 'deleteContentBackward', null, 85, 90)
    assertCounter(w, 'email-subject', 85, 80, 'over-limit')
  })
})

describe('read-only displays show the real count (ui.md §3.13, §3.14 d2)', () => {
  it.each(['readonly', 'locked'])('mode %s: 90/80 subject and 170/160 SMS, not trimmed', (mode) => {
    const subj = 's'.repeat(90)
    const sms = 'm'.repeat(170)
    const we = mountEditor({ mode, channel: 'email', modelValue: content(sms, subj) })
    expect(fieldEl(we, 'email-subject').value).toBe(subj)
    assertCounter(we, 'email-subject', 90, 80, 'over-limit')
    we.unmount()
    wrapper = undefined
    const ws = mountEditor({ mode, channel: 'sms', modelValue: content(sms, subj) })
    expect(fieldEl(ws, 'sms-body').value).toBe(sms)
    assertCounter(ws, 'sms-body', 170, 160, 'over-limit')
    expect(emits(ws)).toHaveLength(0)
  })
})

describe('the cap applies only to the SMS body and the subject (ui.md §3.14 d2)', () => {
  it('the email body is not capped at 5000 (5000 is checked only on validation)', async () => {
    const body = 'g'.repeat(5000)
    const w = mountEditor({ channel: 'email', modelValue: content(undefined, undefined, body) })
    expect(fieldEl(w, 'email-body').hasAttribute('maxlength')).toBe(false)
    await typeText(fieldEl(w, 'email-body'), 'h')
    expect(modelText(w, 'email-body')).toBe(body + 'h')
    await paste(fieldEl(w, 'email-body'), 'i'.repeat(100))
    expect(runes(modelText(w, 'email-body'))).toBe(5101)
  })
})

describe('the worst-case line is separate from the input cap (ui.md §3.14 d2, §3.13)', () => {
  it('a template under the limit still shows a worst-case over-limit, and typing is capped by the template count only', async () => {
    const base = '{{first_name}}' + 'a'.repeat(136) // template 150
    const w = mountEditor({
      modelValue: content(base),
      worstCase: { sms_runes: 186, subject_runes: 42 },
    })
    assertCounter(w, 'sms-body', 150, 160, 'near-limit')
    const worst = w.get(sel('char-counter-worst-sms-body'))
    expect(worst.attributes('data-count')).toBe('186')
    expect(worst.attributes('data-state')).toBe('over-limit')
    await typeText(fieldEl(w, 'sms-body'), 'b')
    expect(modelText(w, 'sms-body')).toBe(base + 'b')
    assertCounter(w, 'sms-body', 151, 160, 'near-limit')
  })
})
