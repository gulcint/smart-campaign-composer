<script setup lang="ts">
// ui.md §3.19 / DESIGN.md §7: pill bar; testid comes from the parent. P10 `size`: sm 6 px (panel
// rows), md 8 px (default, channel cards), lg 12 px (Tracking overall card); ARIA is size-independent.
import { computed } from 'vue'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    value: number
    max: number
    variant?: 'single' | 'segmented'
    segments?: { sent: number; failed: number; sending: number; queued: number } | null
    label?: string
    /** Fill class for `single` (e.g. a channel colour); defaults to the brand gradient. */
    fillClass?: string
    size?: 'sm' | 'md' | 'lg'
  }>(),
  { variant: 'single', segments: null, label: '', fillClass: 'bg-gradient-brand', size: 'md' },
)

const HEIGHT = { sm: 'h-1.5', md: 'h-2', lg: 'h-3' } as const

const pct = (v: number) => (props.max > 0 ? Math.min(100, Math.max(0, (v / props.max) * 100)) : 0)

const parts = computed(() => {
  const s = props.segments
  if (props.variant !== 'segmented' || !s) return []
  return [
    { key: 'sent', w: pct(s.sent), cls: 'bg-success' },
    { key: 'failed', w: pct(s.failed), cls: 'bg-error' },
    { key: 'sending', w: pct(s.sending), cls: 'bg-info' },
  ]
})
</script>

<template>
  <div
    role="progressbar"
    :aria-valuenow="value"
    aria-valuemin="0"
    :aria-valuemax="max"
    :aria-label="label || undefined"
    :class="cn('flex w-full overflow-hidden rounded-pill bg-track', HEIGHT[size])"
  >
    <template v-if="variant === 'segmented'">
      <span
        v-for="p in parts"
        :key="p.key"
        :class="cn('h-full transition-[width] duration-[var(--dur-bar)] ease-standard', p.cls)"
        :style="{ width: `${p.w}%` }"
      />
    </template>
    <span
      v-else
      :class="
        cn(
          'h-full rounded-pill transition-[width] duration-[var(--dur-bar)] ease-standard',
          fillClass,
        )
      "
      :style="{ width: `${pct(value)}%` }"
    />
  </div>
</template>
