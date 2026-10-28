// Mount helper for page tests (docs/contracts/ui.md §0, §1). The route table mirrors ui.md §1 (names,
// paths); only the page under test is real, every other route renders StubPage.
// The tracking route (REQ-TRK-01) is `tracking` at `/campaigns/:campaignId/tracking`; its live stream
// factory is provided with `EventSourceFactoryKey` from `@/api/client` (docs/requirements.md REQ-TRK-01).
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import type { Component } from 'vue'
import { RouterView, createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiClientKey, EventSourceFactoryKey, FetchKey } from '@/api/client'
import { CAMPAIGN_ID } from './p4'
import Stub from './StubPage'

export async function mountPage(
  page: Component,
  routeName: 'content' | 'approval' | 'tracking',
  deps: {
    api: unknown
    fetch?: unknown
    eventSource?: unknown
    query?: Record<string, string>
    campaignId?: string
  },
): Promise<{ wrapper: VueWrapper; router: Router }> {
  const pick = (name: string) => (name === routeName ? page : Stub)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'brief-new', component: Stub },
      { path: '/campaigns/:campaignId', name: 'campaign', component: Stub },
      { path: '/campaigns/:campaignId/brief', name: 'brief', component: Stub, props: true },
      { path: '/campaigns/:campaignId/audience', name: 'audience', component: Stub, props: true },
      {
        path: '/campaigns/:campaignId/content',
        name: 'content',
        component: pick('content'),
        props: true,
      },
      {
        path: '/campaigns/:campaignId/approval',
        name: 'approval',
        component: pick('approval'),
        props: true,
      },
      {
        path: '/campaigns/:campaignId/tracking',
        name: 'tracking',
        component: pick('tracking'),
        props: true,
      },
      { path: '/:pathMatch(.*)*', name: 'not-found', component: Stub },
    ],
  })
  await router.push({
    name: routeName,
    params: { campaignId: deps.campaignId ?? CAMPAIGN_ID },
    query: deps.query ?? {},
  })
  await router.isReady()
  const provide: Record<symbol, unknown> = { [ApiClientKey as symbol]: deps.api }
  if (deps.fetch) provide[FetchKey as symbol] = deps.fetch
  if (deps.eventSource) provide[EventSourceFactoryKey as symbol] = deps.eventSource
  const wrapper = mount(RouterView, {
    global: { plugins: [router, createPinia()], provide },
    attachTo: document.body,
  })
  await flushPromises()
  return { wrapper, router }
}
