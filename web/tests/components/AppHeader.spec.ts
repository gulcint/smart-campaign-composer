// REQ-AI-03, REQ-AUD-00 (UI shell): header with provider badge and campaign state (docs/contracts/ui.md §3.2).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AppHeader from '@/components/AppHeader.vue'
import { t } from '@/i18n/index'

const sel = (id: string) => `[data-testid="${id}"]`

// Campaign states from docs/contracts/ui.md §1 (CampaignState).
const CAMPAIGN_STATES = [
  'draft',
  'audience_ready',
  'content_ready',
  'approved',
  'sending',
  'completed',
] as const

describe('AppHeader', () => {
  it('shows the provider badge and the campaign state badge', () => {
    const w = mount(AppHeader, { props: { provider: 'mock', campaignState: 'audience_ready' } })
    expect(w.find(sel('app-header')).exists()).toBe(true)
    expect(w.get(sel('provider-badge')).attributes('data-provider')).toBe('mock')
    const state = w.get(sel('app-header-campaign-state'))
    expect(state.attributes('data-kind')).toBe('campaign')
    expect(state.attributes('data-status')).toBe('audience_ready')
    expect(state.text()).toContain(t('status.campaign.audience_ready'))
  })

  it.each(CAMPAIGN_STATES)('campaign state %s is shown', (s) => {
    const w = mount(AppHeader, { props: { provider: 'claude', campaignState: s } })
    expect(w.get(sel('app-header-campaign-state')).attributes('data-status')).toBe(s)
  })

  it('has no campaign state badge without a campaign', () => {
    const w = mount(AppHeader, { props: { provider: null, campaignState: null } })
    expect(w.find(sel('app-header-campaign-state')).exists()).toBe(false)
    expect(w.get(sel('provider-badge')).attributes('data-provider')).toBe('unknown')
  })

  it('passes providerLoading to the badge', () => {
    const w = mount(AppHeader, {
      props: { provider: null, providerLoading: true, campaignState: null },
    })
    expect(w.get(sel('provider-badge')).attributes('data-state')).toBe('loading')
  })
})
