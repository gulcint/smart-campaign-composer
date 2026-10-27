<script setup lang="ts">
// ui.md §3.2 / DESIGN.md §8: wordmark left, provider + campaign state right.
import type { CampaignState, LlmProvider } from '@/api/types'
import { t } from '@/i18n'

import ProviderBadge from './ProviderBadge.vue'
import StatusBadge from './StatusBadge.vue'

withDefaults(
  defineProps<{
    provider: LlmProvider | null
    providerLoading?: boolean
    campaignState: CampaignState | null
  }>(),
  { providerLoading: false },
)
</script>

<template>
  <header
    data-testid="app-header"
    class="sticky top-0 z-30 border-b border-border-subtle bg-surface-card/85 backdrop-blur"
  >
    <div class="mx-auto flex h-16 max-w-[1120px] items-center justify-between gap-4 px-4 md:px-6">
      <RouterLink
        :to="{ name: 'brief-new' }"
        class="flex items-center gap-3 text-h4 font-bold text-ink-900 no-underline hover:text-ink-900"
      >
        <!-- Owner decision 2026-10-27: Insider One logo (own dark background, no theme inversion). -->
        <img
          src="/brand/insider-one-logo.jpeg"
          :alt="t('app.logoAlt')"
          width="38"
          height="38"
          loading="eager"
          decoding="async"
          class="size-[38px] shrink-0 rounded-sm"
        />
        <span class="flex items-baseline gap-1.5">
          <span class="hidden sm:inline">{{ t('app.wordmark') }}</span>
          <span class="sm:hidden">{{ t('app.wordmarkShort') }}</span>
          <span class="hidden text-accent-display sm:inline">{{ t('app.wordmarkAccent') }}</span>
        </span>
      </RouterLink>
      <div class="flex items-center gap-2">
        <ProviderBadge :provider="provider" :loading="providerLoading" />
        <StatusBadge
          v-if="campaignState"
          data-testid="app-header-campaign-state"
          kind="campaign"
          :status="campaignState"
          :aria-label="`${t('app.campaignState')}: ${t(`status.campaign.${campaignState}`)}`"
        />
      </div>
    </div>
  </header>
</template>
