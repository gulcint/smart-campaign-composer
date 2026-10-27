// REQ-AUD-00, REQ-AUD-03 (UI flow): step access per campaign state (docs/contracts/ui.md §1, §3.4).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CampaignStepper from '@/components/CampaignStepper.vue'

const sel = (id: string) => `[data-testid="${id}"]`
const STEPS = ['brief', 'audience', 'content', 'approval', 'tracking'] as const
type Step = (typeof STEPS)[number]
type Status = 'completed' | 'active' | 'upcoming' | 'locked'
// Campaign states from docs/contracts/ui.md §1 (CampaignState).
type CampaignState =
  'draft' | 'audience_ready' | 'content_ready' | 'approved' | 'sending' | 'completed'

describe('CampaignStepper', () => {
  it.each<{ state: CampaignState; current: Step; want: Record<Step, Status> }>([
    {
      state: 'draft',
      current: 'brief',
      want: {
        brief: 'active',
        audience: 'locked',
        content: 'locked',
        approval: 'locked',
        tracking: 'locked',
      },
    },
    {
      state: 'audience_ready',
      current: 'audience',
      want: {
        brief: 'completed',
        audience: 'active',
        content: 'upcoming',
        approval: 'locked',
        tracking: 'locked',
      },
    },
    {
      state: 'audience_ready',
      current: 'brief',
      want: {
        brief: 'active',
        audience: 'upcoming',
        content: 'upcoming',
        approval: 'locked',
        tracking: 'locked',
      },
    },
    {
      state: 'content_ready',
      current: 'content',
      want: {
        brief: 'completed',
        audience: 'completed',
        content: 'active',
        approval: 'upcoming',
        tracking: 'locked',
      },
    },
    {
      state: 'approved',
      current: 'approval',
      want: {
        brief: 'completed',
        audience: 'completed',
        content: 'completed',
        approval: 'active',
        tracking: 'locked',
      },
    },
    {
      state: 'sending',
      current: 'tracking',
      want: {
        brief: 'completed',
        audience: 'completed',
        content: 'completed',
        approval: 'completed',
        tracking: 'active',
      },
    },
    {
      state: 'completed',
      current: 'tracking',
      want: {
        brief: 'completed',
        audience: 'completed',
        content: 'completed',
        approval: 'completed',
        tracking: 'active',
      },
    },
  ])('$state on $current', ({ state, current, want }) => {
    const w = mount(CampaignStepper, { props: { campaignState: state, current } })
    expect(w.find(sel('stepper')).exists()).toBe(true)
    for (const step of STEPS) {
      const el = w.get(sel(`stepper-step-${step}`))
      expect([step, el.attributes('data-status')]).toEqual([step, want[step]])
      if (want[step] === 'locked') {
        expect(el.attributes('aria-disabled')).toBe('true')
      } else {
        expect(el.attributes('aria-disabled')).not.toBe('true')
      }
    }
  })

  it('emits navigate for an accessible step', async () => {
    const w = mount(CampaignStepper, {
      props: { campaignState: 'audience_ready', current: 'audience' },
    })
    await w.get(sel('stepper-step-content')).trigger('click')
    await w.get(sel('stepper-step-brief')).trigger('click')
    expect(w.emitted('navigate')).toEqual([['content'], ['brief']])
  })

  it('does not emit navigate for a locked step', async () => {
    const w = mount(CampaignStepper, {
      props: { campaignState: 'audience_ready', current: 'audience' },
    })
    await w.get(sel('stepper-step-approval')).trigger('click')
    await w.get(sel('stepper-step-tracking')).trigger('click')
    expect(w.emitted('navigate')).toBeUndefined()
  })
})
