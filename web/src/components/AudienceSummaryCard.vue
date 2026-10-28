<script setup lang="ts">
// ui.md §3.7 (REQ-AUD-03, REQ-AUD-04): counts per channel after consent filtering.
import { Info, PencilLine, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'

import type { ApiError } from '@/api/errors'
import type { Audience, LlmProvider } from '@/api/types'
import { formatNumber, t } from '@/i18n'

import Button from './Button.vue'
import ErrorCard from './ErrorCard.vue'
import ProgressBar from './ProgressBar.vue'
import Skeleton from './Skeleton.vue'

const props = withDefaults(
  defineProps<{
    audience: Audience | null
    loading: boolean
    error: ApiError | null
    /** LLM building the segment right now; null when only loading (no generation). */
    provider?: LlmProvider | null
  }>(),
  { provider: null },
)
const emit = defineEmits<{ 'edit-brief': []; retry: [] }>()

const state = computed(() => {
  if (props.loading) return 'loading'
  if (props.error) return 'error'
  if (!props.audience) return 'loading'
  return props.audience.matched === 0 ? 'empty' : 'ready'
})

const loadingText = computed(() =>
  t(props.provider === 'claude' ? 'audience.loading.claude' : 'audience.loading'),
)

// Static channel rows for the loading skeleton; labels and colours are known before the data.
const CHANNEL_ROWS = [
  { key: 'sms', label: 'audience.channelSms', dot: 'bg-sms' },
  { key: 'email', label: 'audience.channelEmail', dot: 'bg-email' },
] as const

const share = (n: number) =>
  props.audience && props.audience.matched > 0 ? Math.round((n / props.audience.matched) * 100) : 0

const channels = computed(() => {
  const a = props.audience
  if (!a) return []
  return [
    {
      key: 'sms' as const,
      label: t('audience.channelSms'),
      count: a.sms,
      dot: 'bg-sms',
      fill: 'bg-sms',
    },
    {
      key: 'email' as const,
      label: t('audience.channelEmail'),
      count: a.email,
      dot: 'bg-email',
      fill: 'bg-email',
    },
  ]
})
</script>

<template>
  <section
    data-testid="audience-card"
    :data-state="state"
    :aria-busy="state === 'loading' ? 'true' : undefined"
    class="flex flex-col gap-5"
  >
    <!-- Loading mirrors the ready layout row for row (fixed line heights: numeral-xl 44 px,
         numeral-md 28 px) so the swap to real numbers does not move anything. The visible
         status line sits where the matched label goes and is the only polite announcement. -->
    <template v-if="state === 'loading'">
      <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Skeleton variant="shimmer" class="h-11 w-28 rounded-sm" />
        <span
          data-testid="audience-loading-status"
          role="status"
          class="text-body font-medium text-accent-text"
          >{{ loadingText }}</span
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

    <ErrorCard
      v-else-if="state === 'error' && error"
      :error="error"
      action="retry"
      context="audience"
      @action="emit('retry')"
    />

    <template v-else-if="audience">
      <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span
          data-testid="audience-count-matched"
          :data-value="audience.matched"
          :class="['text-numeral-xl', state === 'empty' ? 'text-warning' : 'text-accent-numeral']"
          >{{ formatNumber(audience.matched) }}</span
        >
        <span class="text-body text-ink-600">{{ t('audience.matchedLabel') }}</span>
      </div>

      <div
        v-if="state === 'empty'"
        data-testid="audience-empty"
        class="flex flex-col gap-3 rounded-card bg-warning-bg p-4 sm:flex-row sm:items-center sm:justify-between"
      >
        <p class="flex items-center gap-2 text-body-sm text-warning">
          <TriangleAlert class="size-4 shrink-0" aria-hidden="true" />
          {{ t('audience.empty') }}
        </p>
        <Button variant="secondary" size="sm" @click="emit('edit-brief')">
          <PencilLine aria-hidden="true" />
          {{ t('audience.editBrief') }}
        </Button>
      </div>

      <dl class="flex flex-col gap-3">
        <div
          v-for="c in channels"
          :key="c.key"
          class="grid grid-cols-[4.5rem_1fr_auto] items-center gap-3 sm:grid-cols-[5rem_1fr_7rem]"
        >
          <dt class="flex items-center gap-2 text-body-sm font-medium text-ink-700">
            <span :class="['size-2 rounded-full', c.dot]" aria-hidden="true" />
            {{ c.label }}
          </dt>
          <ProgressBar
            :data-testid="`audience-bar-${c.key}`"
            :value="c.count"
            :max="audience.matched"
            :fill-class="c.fill"
            :label="
              t('audience.barLabel', {
                channel: c.label,
                count: formatNumber(c.count),
                total: formatNumber(audience.matched),
              })
            "
          />
          <dd class="flex items-baseline justify-end gap-2">
            <span
              :data-testid="`audience-count-${c.key}`"
              :data-value="c.count"
              class="text-numeral-md text-accent-numeral"
              >{{ formatNumber(c.count) }}</span
            >
            <span class="w-10 text-right text-numeral-sm text-ink-500">{{
              t('audience.channelShare', { p: share(c.count) })
            }}</span>
          </dd>
        </div>
      </dl>
    </template>

    <p data-testid="audience-consent-note" class="flex items-start gap-2 text-body-sm text-ink-500">
      <Info class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span>{{ t('audience.consentNote') }}</span>
    </p>
  </section>
</template>
