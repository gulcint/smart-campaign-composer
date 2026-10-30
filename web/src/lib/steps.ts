// Campaign state -> step access and redirect rules (docs/contracts/ui.md §1).
import type { CampaignState, StepId } from '@/api/types'

export const STEPS: readonly StepId[] = ['brief', 'audience', 'content', 'approval', 'tracking']

const ACCESSIBLE: Record<CampaignState, readonly StepId[]> = {
  draft: ['brief'],
  audience_ready: ['brief', 'audience', 'content'],
  content_ready: ['brief', 'audience', 'content', 'approval'],
  approved: ['brief', 'audience', 'content', 'approval'],
  scheduled: STEPS,
  sending: STEPS,
  completed: STEPS,
}

const REDIRECT: Record<CampaignState, StepId> = {
  draft: 'brief',
  audience_ready: 'audience',
  content_ready: 'content',
  approved: 'approval',
  scheduled: 'tracking',
  sending: 'tracking',
  completed: 'tracking',
}

/** Steps a campaign in `state` may open; with no campaign only the brief is open. */
export function accessibleSteps(state: CampaignState | null): readonly StepId[] {
  return state ? ACCESSIBLE[state] : ['brief']
}

export function isStepAccessible(state: CampaignState | null, step: StepId): boolean {
  return accessibleSteps(state).includes(step)
}

/** The step `/campaigns/:id` (and any locked step) redirects to. */
export function redirectStep(state: CampaignState): StepId {
  return REDIRECT[state]
}

export type StepStatus = 'completed' | 'active' | 'upcoming' | 'locked'

export function stepStatus(state: CampaignState | null, current: StepId, step: StepId): StepStatus {
  if (step === current) return 'active'
  if (!isStepAccessible(state, step)) return 'locked'
  return STEPS.indexOf(step) < STEPS.indexOf(current) ? 'completed' : 'upcoming'
}

/** Steps 1–4 are read-only while a send is planned (cancel the plan first) and once it has started. */
export function isLockedForEditing(state: CampaignState | null): boolean {
  return state === 'scheduled' || state === 'sending' || state === 'completed'
}
