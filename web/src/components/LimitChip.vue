<script setup lang="ts">
// Read-only length status for the Approval summary (DESIGN.md §8.4): "label n/limit" with the
// same thresholds as CharCounter (ui.md §3.13: normal < 90 % ≤ near-limit ≤ limit < over-limit).
// Always icon + text + a screen-reader state word, never colour alone.
import { CircleAlert, CircleCheck, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'

import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const props = defineProps<{ label: string; value: number; limit: number }>()

const state = computed(() => {
  if (props.value > props.limit) return 'over-limit'
  if (props.value >= props.limit * 0.9) return 'near-limit'
  return 'normal'
})
const look = computed(
  () =>
    ({
      normal: { icon: CircleCheck, cls: 'bg-success-bg text-success' },
      'near-limit': { icon: TriangleAlert, cls: 'bg-warning-bg text-warning' },
      'over-limit': { icon: CircleAlert, cls: 'bg-error-bg text-error' },
    })[state.value],
)
</script>

<template>
  <span
    :data-state="state"
    :data-value="value"
    :class="
      cn(
        'inline-flex h-7 items-center gap-1.5 rounded-pill px-2.5 text-caption whitespace-nowrap',
        look.cls,
      )
    "
  >
    <component :is="look.icon" class="size-3.5" aria-hidden="true" />
    <span class="font-medium">{{ label }}</span>
    <span class="font-mono font-semibold tabular-nums">{{ value }}/{{ limit }}</span>
    <span class="sr-only">{{ t(`limit.state.${state}`) }}</span>
  </span>
</template>
