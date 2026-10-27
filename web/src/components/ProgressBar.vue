<script setup lang="ts">
// ui.md §3.19 / DESIGN.md §7: 8 px pill bar; testid comes from the parent.
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
  }>(),
  { variant: 'single', segments: null, label: '', fillClass: 'bg-gradient-brand' },
)

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
    class="flex h-2 w-full overflow-hidden rounded-pill bg-track"
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
