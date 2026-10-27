// REQ-AI-03 (UI): "arayüzde Mock LLM rozeti görünür" — the badge reflects the effective provider.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ProviderBadge from '@/components/ProviderBadge.vue'
import { t } from '@/i18n/index'

const sel = (id: string) => `[data-testid="${id}"]`

describe('ProviderBadge', () => {
  it.each([
    { provider: 'mock', want: 'mock', text: 'provider.mock' },
    { provider: 'claude', want: 'claude', text: 'provider.claude' },
  ] as const)('provider $provider => data-provider $want', ({ provider, want, text }) => {
    const w = mount(ProviderBadge, { props: { provider } })
    const badge = w.get(sel('provider-badge'))
    expect(badge.attributes('data-provider')).toBe(want)
    expect(badge.text()).toContain(t(text))
  })
})
