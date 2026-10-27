// Routes and step guards (docs/contracts/ui.md §1).
import type { RouteLocationNormalized, Router, RouterHistory } from 'vue-router'
import { createRouter, createWebHistory } from 'vue-router'

import type { StepId } from '@/api/types'
import { isStepAccessible, redirectStep } from '@/lib/steps'
import { useCampaignStore } from '@/stores/campaign'

declare module 'vue-router' {
  interface RouteMeta {
    step?: StepId
  }
}

export const STEP_ROUTE: Record<StepId, string> = {
  brief: 'brief',
  audience: 'audience',
  content: 'content',
  approval: 'approval',
  tracking: 'tracking',
}

export function campaignIdOf(route: Pick<RouteLocationNormalized, 'params'>): string | null {
  const id = route.params.campaignId
  return typeof id === 'string' && id ? id : null
}

export function createAppRouter(history: RouterHistory = createWebHistory()): Router {
  const router = createRouter({
    history,
    routes: [
      {
        path: '/',
        name: 'brief-new',
        component: () => import('@/pages/BriefPage.vue'),
        meta: { step: 'brief' },
      },
      {
        path: '/campaigns/:campaignId',
        name: 'campaign',
        // Always redirected by the guard below; the component is never shown.
        component: () => import('@/pages/BriefPage.vue'),
      },
      {
        path: '/campaigns/:campaignId/brief',
        name: 'brief',
        component: () => import('@/pages/BriefPage.vue'),
        meta: { step: 'brief' },
      },
      {
        path: '/campaigns/:campaignId/audience',
        name: 'audience',
        component: () => import('@/pages/AudiencePage.vue'),
        meta: { step: 'audience' },
      },
      {
        path: '/campaigns/:campaignId/content',
        name: 'content',
        component: () => import('@/pages/ContentPage.vue'),
        meta: { step: 'content' },
      },
      {
        path: '/campaigns/:campaignId/approval',
        name: 'approval',
        component: () => import('@/pages/ApprovalPage.vue'),
        meta: { step: 'approval' },
      },
      {
        path: '/campaigns/:campaignId/tracking',
        name: 'tracking',
        component: () => import('@/pages/TrackingPage.vue'),
        meta: { step: 'tracking' },
      },
      {
        path: '/:pathMatch(.*)*',
        name: 'not-found',
        component: () => import('@/pages/NotFoundPage.vue'),
      },
    ],
  })

  // `/campaigns/:id` goes to the step for the campaign state; a locked step goes there too.
  // An unknown campaign stays on its route, where the page shows EmptyState (404).
  router.beforeEach(async (to) => {
    const id = campaignIdOf(to)
    if (!id) return true
    const store = useCampaignStore()
    const campaign = await store.ensure(id)
    if (!campaign) {
      return to.name === 'campaign' ? { name: 'brief', params: { campaignId: id } } : true
    }
    const target = { name: STEP_ROUTE[redirectStep(campaign.state)], params: { campaignId: id } }
    if (to.name === 'campaign') return target
    const step = to.meta.step
    if (step && !isStepAccessible(campaign.state, step)) return target
    return true
  })

  return router
}
