// REQ-AUD-05 (UI): BriefComposer counter, limit and submit rules (docs/contracts/ui.md §3.5).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BriefComposer from '@/components/BriefComposer.vue'

const EXAMPLE =
  "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

const sel = (id: string) => `[data-testid="${id}"]`

function mountComposer(props: Record<string, unknown> = {}) {
  return mount(BriefComposer, { props: { modelValue: '', ...props } })
}

describe('BriefComposer counter (REQ-AUD-05)', () => {
  it.each([
    { name: 'empty brief counts 0', value: '', n: 0 },
    { name: 'example brief counts its code points', value: EXAMPLE, n: [...EXAMPLE].length },
    { name: 'surrounding whitespace is not counted', value: '  \n merhaba \t ', n: 7 },
    { name: 'Turkish letters count one each', value: 'şğüİı', n: 5 },
    { name: 'decomposed letters count after NFC', value: 'ş'.repeat(3), n: 3 },
  ])('$name', ({ value, n }) => {
    const w = mountComposer({ modelValue: value })
    const counter = w.get(sel('brief-counter'))
    expect(counter.attributes('data-count')).toBe(String(n))
    expect(counter.text()).toContain(`${n}/2000`)
  })

  it.each([
    { n: 1, state: 'normal' },
    { n: 1799, state: 'normal' },
    { n: 1800, state: 'near-limit' },
    { n: 2000, state: 'near-limit' },
    { n: 2001, state: 'over-limit' },
  ])('$n chars => data-state $state', ({ n, state }) => {
    const w = mountComposer({ modelValue: 'a'.repeat(n) })
    expect(w.get(sel('brief-counter')).attributes('data-state')).toBe(state)
  })
})

describe('BriefComposer submit (REQ-AUD-05)', () => {
  it.each([
    { name: 'empty brief', props: { modelValue: '' } },
    { name: 'whitespace-only brief', props: { modelValue: '   \n\t ' } },
    { name: '2001 chars', props: { modelValue: 'a'.repeat(2001) } },
    { name: 'while submitting', props: { modelValue: EXAMPLE, submitting: true } },
  ])('is disabled for $name and does not emit submit', async ({ props }) => {
    const w = mountComposer(props)
    const btn = w.get(sel('brief-submit'))
    expect(btn.attributes('aria-disabled')).toBe('true')
    await btn.trigger('click')
    await w.get(sel('brief-input')).trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(w.emitted('submit')).toBeUndefined()
  })

  it.each([
    { name: 'the example brief', value: EXAMPLE },
    { name: 'exactly 2000 chars', value: 'ş'.repeat(2000) },
    { name: '2000 chars with surrounding spaces', value: `  ${'a'.repeat(2000)}  ` },
  ])('is enabled for $name and emits submit once', async ({ value }) => {
    const w = mountComposer({ modelValue: value })
    const btn = w.get(sel('brief-submit'))
    expect(btn.attributes('aria-disabled')).not.toBe('true')
    await btn.trigger('click')
    const emitted = w.emitted('submit')
    expect(emitted).toHaveLength(1)
    const payload = emitted?.[0]?.[0]
    expect(typeof payload).toBe('string')
    expect(String(payload).normalize('NFC').trim()).toBe(value.normalize('NFC').trim())
  })

  it('Ctrl+Enter and Cmd+Enter submit; plain Enter does not', async () => {
    const w = mountComposer({ modelValue: EXAMPLE })
    const input = w.get(sel('brief-input'))
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('submit')).toBeUndefined()
    await input.trigger('keydown', { key: 'Enter', ctrlKey: true })
    expect(w.emitted('submit')).toHaveLength(1)
    await input.trigger('keydown', { key: 'Enter', metaKey: true })
    expect(w.emitted('submit')).toHaveLength(2)
  })
})

describe('BriefComposer input (REQ-AUD-05)', () => {
  it('typing emits update:modelValue', async () => {
    const w = mountComposer({ modelValue: '' })
    await w.get(sel('brief-input')).setValue('Merhaba')
    const ev = w.emitted('update:modelValue')
    expect(ev).toBeDefined()
    const last = ev ? ev[ev.length - 1] : undefined
    expect(last?.[0]).toBe('Merhaba')
  })

  it.each([
    { name: 'readonly', props: { readonly: true } },
    { name: 'submitting', props: { submitting: true } },
  ])('textarea is readonly when $name', ({ props }) => {
    const w = mountComposer({ modelValue: EXAMPLE, ...props })
    expect(w.get(sel('brief-input')).attributes('readonly')).toBeDefined()
  })

  it('textarea is editable by default', () => {
    const w = mountComposer({ modelValue: EXAMPLE })
    expect(w.get(sel('brief-input')).attributes('readonly')).toBeUndefined()
  })

  it('the example button emits use-example', async () => {
    const w = mountComposer()
    await w.get(sel('brief-example')).trigger('click')
    expect(w.emitted('use-example')).toHaveLength(1)
  })
})

describe('BriefComposer field errors (REQ-AUD-05, ui.md §4 422/413)', () => {
  it.each([
    { code: 'required', limit: null, actual: null },
    { code: 'too_long', limit: 2000, actual: 2001 },
    { code: 'payload_too_large', limit: null, actual: null },
  ])('shows inline-error-brief for code $code', ({ code, limit, actual }) => {
    const fe: Record<string, unknown> = { path: 'brief', code, message: 'x' }
    if (limit !== null) fe.limit = limit
    if (actual !== null) fe.actual = actual
    const w = mountComposer({ modelValue: '', fieldErrors: [fe] })
    const err = w.get(sel('inline-error-brief'))
    expect(err.attributes('data-code')).toBe(code)
  })

  it('shows no inline-error-brief without a brief error', () => {
    const w = mountComposer({
      modelValue: EXAMPLE,
      fieldErrors: [{ path: 'header.X-Mock-LLM-Scenario', code: 'invalid_value', message: 'x' }],
    })
    expect(w.find(sel('inline-error-brief')).exists()).toBe(false)
  })

  it('renders the brief as text, never as HTML (REQ-UI-01)', () => {
    const P = '<script>alert(1)</script><b>x</b>'
    const w = mountComposer({ modelValue: P })
    const input = w.get(sel('brief-input')).element as HTMLTextAreaElement
    expect(input.value).toBe(P)
    expect(w.element.querySelector('script')).toBeNull()
    expect(w.element.querySelector('b')).toBeNull()
  })
})
