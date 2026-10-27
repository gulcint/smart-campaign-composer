// REQ-CNT-01, REQ-CNT-02, REQ-CNT-05 (UI): CharCounter thresholds and the worst-case line
// (docs/contracts/ui.md §3.13).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CharCounter from '@/components/CharCounter.vue'
import { t } from '@/i18n/index'
import { sel } from '../helpers/p4'

function mountCounter(
  field: 'sms-body' | 'email-subject',
  text: string,
  worstCase: number | null = null,
) {
  return mount(CharCounter, {
    props: { field, text, limit: field === 'sms-body' ? 160 : 80, worstCase },
  })
}

describe('CharCounter (REQ-CNT-01, REQ-CNT-02)', () => {
  it.each([
    { field: 'sms-body', n: 143, state: 'normal' },
    { field: 'sms-body', n: 144, state: 'near-limit' },
    { field: 'sms-body', n: 160, state: 'near-limit' },
    { field: 'sms-body', n: 161, state: 'over-limit' },
    { field: 'email-subject', n: 71, state: 'normal' },
    { field: 'email-subject', n: 72, state: 'near-limit' },
    { field: 'email-subject', n: 80, state: 'near-limit' },
    { field: 'email-subject', n: 81, state: 'over-limit' },
  ] as const)('$field with $n chars is $state', ({ field, n, state }) => {
    const w = mountCounter(field, 'ş'.repeat(n))
    const root = w.get(sel(`char-counter-${field}`))
    expect(root.attributes('data-state')).toBe(state)
    expect(root.attributes('aria-live')).toBe('polite')
    const value = w.get(sel(`char-counter-value-${field}`))
    expect(value.attributes('data-count')).toBe(String(n))
    const limit = field === 'sms-body' ? 160 : 80
    expect(value.text()).toContain(`${n}/${limit}`)
    expect(w.find(sel(`char-counter-over-${field}`)).exists()).toBe(state === 'over-limit')
  })

  it('shows how far over the limit the text is', () => {
    const w = mountCounter('sms-body', 'a'.repeat(163))
    expect(w.get(sel('char-counter-over-sms-body')).text()).toContain('3')
  })

  it('counts code points after NFC (decomposed letters count once) and variables as written', () => {
    expect(
      mountCounter('sms-body', 'ş'.repeat(10))
        .get(sel('char-counter-value-sms-body'))
        .attributes('data-count'),
    ).toBe('10')
    expect(
      mountCounter('sms-body', '{{first_name}}')
        .get(sel('char-counter-value-sms-body'))
        .attributes('data-count'),
    ).toBe('14')
  })
})

describe('CharCounter worst-case line (REQ-CNT-05)', () => {
  it('is absent without a worst case', () => {
    expect(mountCounter('sms-body', 'a').find(sel('char-counter-worst-sms-body')).exists()).toBe(
      false,
    )
  })

  it.each([
    { wc: 143, state: 'normal' },
    { wc: 160, state: 'near-limit' },
    { wc: 161, state: 'over-limit' },
  ])('worst case $wc is $state and uses counter.worstCase', ({ wc, state }) => {
    const w = mountCounter('sms-body', '{{first_name}} merhaba', wc)
    const line = w.get(sel('char-counter-worst-sms-body'))
    expect(line.attributes('data-count')).toBe(String(wc))
    expect(line.attributes('data-state')).toBe(state)
    expect(line.text()).toBe(t('counter.worstCase', { n: wc }))
  })
})
