<script setup lang="ts">
// ui.md §3.12: `insert` chips add a variable to the editor; `inline` shows an unrendered
// variable; `invalid` marks a variable the backend will reject (presentation only).
import { CircleAlert, Plus } from '@lucide/vue'
import { computed } from 'vue'

import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    name: string
    variant: 'insert' | 'inline' | 'invalid'
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{ insert: [name: string] }>()

const label = computed(() => `{{${props.name}}}`)

function onClick() {
  if (props.variant !== 'insert' || props.disabled) return
  emit('insert', props.name)
}
</script>

<template>
  <button
    v-if="variant === 'insert'"
    type="button"
    :data-testid="`variable-chip-${name}`"
    :data-variant="variant"
    :aria-disabled="disabled ? 'true' : undefined"
    :class="
      cn(
        'inline-flex h-7 items-center gap-1 rounded-pill border border-brand-line bg-secondary px-2.5 font-mono max-lg:h-11 max-lg:px-3.5 text-caption text-secondary-foreground',
        'transition-colors hover:border-brand-icon focus-visible:shadow-focus',
        'aria-disabled:cursor-not-allowed aria-disabled:opacity-45 aria-disabled:hover:border-brand-line',
      )
    "
    @mousedown.prevent
    @click="onClick"
  >
    <Plus class="size-3.5" aria-hidden="true" />
    <span>{{ label }}</span>
  </button>
  <span
    v-else
    :data-testid="`variable-chip-${name}`"
    :data-variant="variant"
    :class="
      cn(
        'inline-flex h-6 items-center gap-1 rounded-pill border px-2 font-mono text-caption',
        variant === 'invalid'
          ? 'border-error bg-error-bg text-error-strong'
          : 'border-border-default bg-surface-sunken text-ink-700',
      )
    "
  >
    <CircleAlert v-if="variant === 'invalid'" class="size-3.5" aria-hidden="true" />
    <span>{{ label }}</span>
  </span>
</template>
