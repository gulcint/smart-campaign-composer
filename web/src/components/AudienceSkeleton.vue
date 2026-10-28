<script setup lang="ts">
// ui.md §3.7a: loading body of the audience card, shared by AudienceSummaryCard (Audience step)
// and AudienceLoadingPreview (Brief step). Mirrors the ready layout row for row with fixed
// heights (numeral-xl 44 px, numeral-md 28 px), so the swap to real numbers does not move.
// The visible status line sits where the matched label goes and is the only announcement.
import type { LlmProvider } from '@/api/types'
import { t } from '@/i18n'

import Skeleton from './Skeleton.vue'

const props = withDefaults(defineProps<{ provider?: LlmProvider | null }>(), { provider: null })

// Labels and colours are known before the data.
const CHANNEL_ROWS = [
  { key: 'sms', label: 'audience.channelSms', dot: 'bg-sms' },
  { key: 'email', label: 'audience.channelEmail', dot: 'bg-email' },
] as const
</script>

<template>
  <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
    <Skeleton variant="shimmer" class="h-11 w-28 rounded-sm" />
    <span
      data-testid="audience-loading-status"
      role="status"
      class="text-body font-medium text-accent-text"
      >{{ t(props.provider === 'claude' ? 'audience.loading.claude' : 'audience.loading') }}</span
    >
  </div>
  <div class="flex flex-col gap-3" aria-hidden="true">
    <div
      v-for="c in CHANNEL_ROWS"
      :key="c.key"
      class="grid grid-cols-[4.5rem_1fr_auto] items-center gap-3 sm:grid-cols-[5rem_1fr_7rem]"
    >
      <span class="flex items-center gap-2 text-body-sm font-medium text-ink-700">
        <span :class="['size-2 rounded-full', c.dot]" />
        {{ t(c.label) }}
      </span>
      <Skeleton variant="shimmer" class="h-2 w-full" />
      <span class="flex h-7 items-center justify-end gap-2">
        <Skeleton variant="shimmer" class="h-5 w-12 rounded-sm" />
        <Skeleton variant="shimmer" class="h-3 w-8" />
      </span>
    </div>
  </div>
</template>
