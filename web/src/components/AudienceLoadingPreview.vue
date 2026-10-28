<script setup lang="ts">
// ui.md §3.7b / DESIGN.md §8.1: Brief step, while the segment is being generated. The same
// card, rule row and audience skeleton the Audience step shows, so the hand-off is continuous.
import { Info } from '@lucide/vue'

import type { LlmProvider } from '@/api/types'
import { t } from '@/i18n'

import AudienceSkeleton from './AudienceSkeleton.vue'
import SegmentRuleSkeleton from './SegmentRuleSkeleton.vue'

withDefaults(defineProps<{ provider?: LlmProvider | null }>(), { provider: null })
</script>

<template>
  <section
    data-testid="brief-audience-preview"
    aria-busy="true"
    :aria-label="t('audience.title')"
    class="flex flex-col gap-5 rounded-card border border-border-default bg-surface-card p-4 shadow-card md:p-6"
  >
    <SegmentRuleSkeleton />
    <div class="h-px bg-border-subtle" aria-hidden="true" />
    <div class="flex flex-col gap-5">
      <AudienceSkeleton :provider="provider" />
      <p class="flex items-start gap-2 text-body-sm text-ink-500">
        <Info class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <span>{{ t('audience.consentNote') }}</span>
      </p>
    </div>
  </section>
</template>
