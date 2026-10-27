// REQ-AI-03, REQ-AI-02 (UI): the "Mock LLM" badge (docs/contracts/ui.md §3.3).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ProviderBadge from '@/components/ProviderBadge.vue'
import { t } from '@/i18n/index'

const sel = (id: string) => `[data-testid="${id}"]`

describe('ProviderBadge', () => {
  it.each([
    { provider: 'mock', want: 'mock', text: 'provider.mock' },
    { provider: 'claude', want: 'claude', text: 'provider.claude' },
    { provider: null, want: 'unknown', text: 'provider.unknown' },
  ] as const)('provider $provider => data-provider $want', ({ provider, want, text }) => {
    const w = mount(ProviderBadge, { props: { provider } })
    const badge = w.get(sel('provider-badge'))
    expect(badge.attributes('data-provider')).toBe(want)
    expect(badge.attributes('data-state')).toBe('ready')
    expect(badge.text()).toContain(t(text))
  })

  it('loading state', () => {
    const w = mount(ProviderBadge, { props: { provider: null, loading: true } })
    expect(w.get(sel('provider-badge')).attributes('data-state')).toBe('loading')
  })
})
