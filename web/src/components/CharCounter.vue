<script setup lang="ts">
// ui.md §3.13 (DESIGN V5): code points after NFC, variables counted as written; the second
// line shows the API's worst case (length after personalisation) when it is known.
import { TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'

import { t } from '@/i18n'
import { cn } from '@/lib/utils'

type CounterState = 'normal' | 'near-limit' | 'over-limit'

const props = withDefaults(
  defineProps<{
    field: 'sms-body' | 'email-subject'
    text: string
    limit: 160 | 80
    worstCase?: number | null
  }>(),
  { worstCase: null },
)

function stateOf(n: number): CounterState {
  if (n > props.limit) return 'over-limit'
  return n >= 0.9 * props.limit ? 'near-limit' : 'normal'
}

const count = computed(() => [...props.text.normalize('NFC')].length)
const state = computed(() => stateOf(count.value))
const worstState = computed(() => (props.worstCase === null ? null : stateOf(props.worstCase)))

const toneClass = (s: CounterState | null) =>
  cn(
    s === 'normal' && 'text-ink-500',
    s === 'near-limit' && 'text-warning',
    s === 'over-limit' && 'text-error',
  )
</script>

<template>
  <div
    :id="`char-counter-${field}`"
    :data-testid="`char-counter-${field}`"
    :data-state="state"
    aria-live="polite"
    class="flex flex-col items-end gap-0.5 text-right"
  >
    <p :class="cn('flex items-center gap-1 text-numeral-sm', toneClass(state))">
      <TriangleAlert v-if="state !== 'normal'" class="size-3.5" aria-hidden="true" />
      <span :data-testid="`char-counter-value-${field}`" :data-count="count"
        >{{ count }}/{{ limit }}</span
      >
      <span
        v-if="state === 'over-limit'"
        :data-testid="`char-counter-over-${field}`"
        class="font-semibold"
        >−{{ count - limit }}</span
      >
    </p>
    <p
      v-if="worstCase !== null"
      :data-testid="`char-counter-worst-${field}`"
      :data-count="worstCase"
      :data-state="worstState"
      :class="cn('text-caption', toneClass(worstState))"
    >
      {{ t('counter.worstCase', { n: worstCase }) }}
    </p>
  </div>
</template>
