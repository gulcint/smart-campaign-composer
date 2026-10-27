<script setup lang="ts">
// App shell (ui.md §2): AppHeader + CampaignStepper (campaign routes) + ConnectionBanner
// + RouterView + Toaster. GET /api/config runs once at start-up.
import { computed, nextTick, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { StepId } from '@/api/types'
import AppHeader from '@/components/AppHeader.vue'
import CampaignStepper from '@/components/CampaignStepper.vue'
import ConnectionBanner from '@/components/ConnectionBanner.vue'
import Toaster from '@/components/Toaster.vue'
import { t } from '@/i18n'
import { campaignIdOf, STEP_ROUTE } from '@/router'
import { useCampaignStore } from '@/stores/campaign'
import { useConfigStore } from '@/stores/config'
import { useUiStore } from '@/stores/ui'

const route = useRoute()
const router = useRouter()
const config = useConfigStore()
const campaigns = useCampaignStore()
const ui = useUiStore()

const campaignId = computed(() => campaignIdOf(route))
const campaignState = computed(() =>
  campaignId.value && campaigns.campaign?.id === campaignId.value ? campaigns.campaign.state : null,
)
const currentStep = computed<StepId>(() => route.meta.step ?? 'brief')

function onNavigate(step: StepId) {
  if (!campaignId.value || step === currentStep.value) return
  void router.push({ name: STEP_ROUTE[step], params: { campaignId: campaignId.value } })
}

onMounted(() => {
  void config.load()
})

// Move focus to the new page's h1 on step changes (DESIGN.md §9), not on first load.
let first = true
router.afterEach((to, from) => {
  if (first) {
    first = false
    return
  }
  if (to.fullPath === from.fullPath) return
  void nextTick(() => {
    document.querySelector<HTMLElement>('[data-testid="page-title"]')?.focus()
  })
})
</script>

<template>
  <a
    href="#main"
    class="sr-only-focusable fixed top-2 left-2 z-50 rounded-pill bg-surface-raised px-4 py-2 shadow-raised"
    >{{ t('app.skipToContent') }}</a
  >
  <AppHeader
    :provider="config.provider"
    :provider-loading="config.loading"
    :campaign-state="campaignState"
  />
  <ConnectionBanner :visible="ui.connectionLost" />
  <div v-if="campaignId" class="border-b border-border-subtle bg-surface-card/50">
    <div class="mx-auto max-w-[1120px] px-4 py-3 md:px-6">
      <CampaignStepper
        :campaign-state="campaignState"
        :current="currentStep"
        @navigate="onNavigate"
      />
    </div>
  </div>
  <main id="main" class="mx-auto w-full max-w-[1120px] px-4 pt-4 pb-24 md:px-6">
    <RouterView />
  </main>
  <Toaster />
</template>
