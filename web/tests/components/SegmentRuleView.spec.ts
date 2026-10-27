// REQ-AUD-00, REQ-AI-01 (UI): the structured segment rule is shown as chips and as JSON
// (docs/contracts/ui.md §3.8, §6.1).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SegmentRuleView from '@/components/SegmentRuleView.vue'

const sel = (id: string) => `[data-testid="${id}"]`

const exampleRule = {
  all: [
    { field: 'city', op: 'eq', value: 'İstanbul' },
    { field: 'last_cart_add_at', op: 'within_last', value: { days: 30 } },
    { field: 'orders_30d', op: 'eq', value: 0 },
  ],
}

const nestedRule = {
  any: [
    { field: 'loyalty_tier', op: 'in', value: ['gold', 'platinum'] },
    {
      all: [
        { field: 'city', op: 'neq', value: 'Ankara' },
        { not: { field: 'last_purchase_at', op: 'is_null', value: null } },
      ],
    },
  ],
}

async function openJson(w: ReturnType<typeof mount>) {
  if (!w.find(sel('segment-rule-json')).exists()) {
    await w.get(sel('segment-rule-json-toggle')).trigger('click')
  }
  return w.get(sel('segment-rule-json'))
}

describe('SegmentRuleView (REQ-AUD-00)', () => {
  it('one chip per condition with data-field and data-op', () => {
    const w = mount(SegmentRuleView, { props: { rule: exampleRule } })
    expect(w.find(sel('segment-rule')).exists()).toBe(true)
    const chips = w.findAll(sel('segment-rule-chip'))
    expect(chips.map((c) => [c.attributes('data-field'), c.attributes('data-op')])).toEqual([
      ['city', 'eq'],
      ['last_cart_add_at', 'within_last'],
      ['orders_30d', 'eq'],
    ])
  })

  it('counts conditions inside nested groups', () => {
    const w = mount(SegmentRuleView, { props: { rule: nestedRule } })
    const fields = w.findAll(sel('segment-rule-chip')).map((c) => c.attributes('data-field'))
    expect(fields.sort()).toEqual(['city', 'last_purchase_at', 'loyalty_tier'])
  })

  it('the toggle shows the rule as pretty-printed JSON and hides it again', async () => {
    const w = mount(SegmentRuleView, { props: { rule: exampleRule } })
    const json = await openJson(w)
    expect(json.element.tagName).toBe('PRE')
    expect(json.element.textContent).toBe(JSON.stringify(exampleRule, null, 2))
    await w.get(sel('segment-rule-json-toggle')).trigger('click')
    expect(w.find(sel('segment-rule-json')).exists()).toBe(false)
  })

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
