<script setup lang="ts">
// ui.md §3.22: loading placeholder; only its presence is asserted.
// `variant="shimmer"` (DESIGN.md §5): brand-orange sweep over the track, static tint under
// reduced motion. Callers give it a fixed height so the swap to real content does not jump.
import type { HTMLAttributes } from 'vue'

import { Skeleton as UiSkeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{ class?: HTMLAttributes['class']; variant?: 'pulse' | 'shimmer' }>(),
  { class: undefined, variant: 'pulse' },
)
</script>

<template>
  <UiSkeleton
    data-testid="skeleton"
    :data-variant="props.variant"
    aria-hidden="true"
    :class="
      cn(
        'rounded-pill bg-track',
        props.variant === 'shimmer' && 'skeleton-shimmer animate-none',
        props.class,
      )
    "
  />
</template>
