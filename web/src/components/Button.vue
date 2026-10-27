<script setup lang="ts">
// ui.md §3.1: disabled keeps the button focusable (aria-disabled, no `disabled` attribute)
// and never emits `click` while disabled or loading.
import { LoaderCircle } from '@lucide/vue'

import { Button as UiButton } from '@/components/ui/button'

const props = withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'ghost' | 'danger-ghost'
    size?: 'sm' | 'md' | 'lg'
    loading?: boolean
    disabled?: boolean
    type?: 'button' | 'submit'
  }>(),
  { variant: 'primary', size: 'md', loading: false, disabled: false, type: 'button' },
)

const emit = defineEmits<{ click: [event: MouseEvent] }>()

function onClick(event: MouseEvent) {
  if (props.disabled || props.loading) {
    event.preventDefault()
    event.stopPropagation()
    return
  }
  emit('click', event)
}
</script>

<template>
  <UiButton
    :variant="variant"
    :size="size"
    :type="type"
    :aria-disabled="disabled ? 'true' : undefined"
    :aria-busy="loading ? 'true' : undefined"
    :data-loading="loading ? 'true' : undefined"
    @click="onClick"
  >
    <LoaderCircle v-if="loading" class="animate-spin" aria-hidden="true" />
    <slot />
  </UiButton>
</template>
