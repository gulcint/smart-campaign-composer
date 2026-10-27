<script setup lang="ts">
// ui.md §3.22: loading placeholder; only its presence is asserted.
import type { HTMLAttributes } from 'vue'

import { Skeleton as UiSkeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

const props = defineProps<{ class?: HTMLAttributes['class'] }>()
</script>

<template>
  <UiSkeleton
    data-testid="skeleton"
    aria-hidden="true"
    :class="cn('rounded-pill bg-track', props.class)"
  />
</template>
