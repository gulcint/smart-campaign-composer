// REQ-CNT-01, REQ-CNT-02 (UI): the counter shows SMS 160/161 and subject 80/81 in runes
// (Turkish letters count once) and flags the limit overrun.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CharCounter from '@/components/CharCounter.vue'
import { sel } from '../helpers/p4'

function mountCounter(field: 'sms-body' | 'email-subject', text: string) {
  return mount(CharCounter, {
    props: { field, text, limit: field === 'sms-body' ? 160 : 80, worstCase: null },
  })
}

describe('CharCounter (REQ-CNT-01, REQ-CNT-02)', () => {
  it.each([
    { field: 'sms-body', n: 160, over: false },
    { field: 'sms-body', n: 161, over: true },
    { field: 'email-subject', n: 80, over: false },
    { field: 'email-subject', n: 81, over: true },
  ] as const)('$field with $n chars: over-limit $over', ({ field, n, over }) => {
    const w = mountCounter(field, 'ş'.repeat(n))
    const root = w.get(sel(`char-counter-${field}`))
    expect(root.attributes('data-state') === 'over-limit').toBe(over)
    const value = w.get(sel(`char-counter-value-${field}`))
    expect(value.attributes('data-count')).toBe(String(n))
    const limit = field === 'sms-body' ? 160 : 80
    expect(value.text()).toContain(`${n}/${limit}`)
    expect(w.find(sel(`char-counter-over-${field}`)).exists()).toBe(over)
  })
})
