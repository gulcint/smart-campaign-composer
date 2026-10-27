// REQ-UI-01: rule values (LLM output) are rendered as text, never as HTML.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SegmentRuleView from '@/components/SegmentRuleView.vue'

const sel = (id: string) => `[data-testid="${id}"]`

async function openJson(w: ReturnType<typeof mount>) {
  if (!w.find(sel('segment-rule-json')).exists()) {
    await w.get(sel('segment-rule-json-toggle')).trigger('click')
  }
  return w.get(sel('segment-rule-json'))
}

describe('SegmentRuleView', () => {
  it('rule values are rendered as text, never as HTML (REQ-UI-01)', async () => {
    const P = '<script>alert(1)</script><b>x</b>'
    const rule = { all: [{ field: 'city', op: 'eq', value: P }] }
    const w = mount(SegmentRuleView, { props: { rule } })
    const chip = w.get(sel('segment-rule-chip'))
    expect(chip.element.textContent).toContain(P)
    const json = await openJson(w)
    expect(json.element.textContent).toBe(JSON.stringify(rule, null, 2))
    expect(w.element.querySelector('script')).toBeNull()
    expect(w.element.querySelector('b')).toBeNull()
  })
})
