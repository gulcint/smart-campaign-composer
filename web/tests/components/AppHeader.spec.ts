// REQ-AI-03 (UI): the header shows the "Mock LLM" provider badge.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AppHeader from '@/components/AppHeader.vue'

const sel = (id: string) => `[data-testid="${id}"]`

describe('AppHeader', () => {
  it('shows the provider badge', () => {
    const w = mount(AppHeader, { props: { provider: 'mock', campaignState: 'audience_ready' } })
    expect(w.find(sel('app-header')).exists()).toBe(true)
    expect(w.get(sel('provider-badge')).attributes('data-provider')).toBe('mock')
  })
})
