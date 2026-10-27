// REQ-AUD-05 (UI): an empty or over-2000-character brief cannot be submitted (so the LLM is not
// called); a valid one up to 2000 characters can. REQ-UI-01: the brief is shown as text.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BriefComposer from '@/components/BriefComposer.vue'

const EXAMPLE =
  "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun."

const sel = (id: string) => `[data-testid="${id}"]`

function mountComposer(props: Record<string, unknown> = {}) {
  return mount(BriefComposer, { props: { modelValue: '', ...props } })
}

describe('BriefComposer submit (REQ-AUD-05)', () => {
  it.each([
    { name: 'empty brief', props: { modelValue: '' } },
    { name: 'whitespace-only brief', props: { modelValue: '   \n\t ' } },
    { name: '2001 chars', props: { modelValue: 'a'.repeat(2001) } },
  ])('is disabled for $name and does not emit submit', async ({ props }) => {
    const w = mountComposer(props)
    const btn = w.get(sel('brief-submit'))
    expect(btn.attributes('aria-disabled')).toBe('true')
    await btn.trigger('click')
    expect(w.emitted('submit')).toBeUndefined()
  })

  it.each([
    { name: 'the example brief', value: EXAMPLE },
    { name: 'exactly 2000 Turkish letters', value: 'ş'.repeat(2000) },
  ])('is enabled for $name and emits submit once', async ({ value }) => {
    const w = mountComposer({ modelValue: value })
    const btn = w.get(sel('brief-submit'))
    expect(btn.attributes('aria-disabled')).not.toBe('true')
    await btn.trigger('click')
    expect(w.emitted('submit')).toHaveLength(1)
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
