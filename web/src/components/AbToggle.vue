<script setup lang="ts">
// ui.md §3.29 (REQ-ABT-04): the "A/B testi" switch next to generate / regenerate. It only
// shapes the next generation request ({"ab":true}); the current version is left alone.
// Hand-rolled switch (WAI-ARIA `switch`) for the same reason as Button: disabled keeps it
// focusable via aria-disabled (no `disabled` attribute) and a click then changes nothing.
// The page's testid lands on the switch itself (inheritAttrs off, $attrs bound to the button).
import { FlaskConical } from '@lucide/vue'
import { useId } from 'vue'

import { t } from '@/i18n'
import { cn } from '@/lib/utils'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{ modelValue: boolean; disabled?: boolean }>(), {
  disabled: false,
})
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const uid = useId()
const labelId = `${uid}-label`
const hintId = `${uid}-hint`

function toggle(e: MouseEvent) {
  if (props.disabled) {
    e.preventDefault()
    return
  }
  emit('update:modelValue', !props.modelValue)
}
</script>

<template>
  <div class="flex min-w-0 items-start gap-3">
    <button
      v-bind="$attrs"
      type="button"
      role="switch"
      :aria-checked="modelValue ? 'true' : 'false'"
      :aria-disabled="disabled ? 'true' : undefined"
      :aria-labelledby="labelId"
      :aria-describedby="hintId"
      :data-state="modelValue ? 'checked' : 'unchecked'"
      :class="
        cn(
          'group relative mt-0.5 inline-flex h-6 w-11 shrink-0 items-center rounded-pill border-2 border-transparent',
          'transition-colors duration-150 outline-none focus-visible:shadow-focus motion-reduce:transition-none',
          'max-lg:my-2.5',
          modelValue ? 'bg-accent-text' : 'bg-ink-300',
          disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer',
        )
      "
      @click="toggle"
    >
      <span
        aria-hidden="true"
        :class="
          cn(
            'pointer-events-none flex size-5 items-center justify-center rounded-full bg-surface-card shadow-card',
            'transition-transform duration-150 motion-reduce:transition-none',
            modelValue ? 'translate-x-5' : 'translate-x-0',
          )
        "
      >
        <FlaskConical
          :class="cn('size-3', modelValue ? 'text-accent-text' : 'text-ink-500')"
          aria-hidden="true"
        />
      </span>
    </button>
    <span class="flex min-w-0 flex-col">
      <span
        :id="labelId"
        :class="cn('text-body-sm font-semibold', disabled ? 'text-ink-500' : 'text-ink-900')"
        >{{ t('content.abToggle') }}</span
      >
      <span :id="hintId" class="text-caption text-ink-500">{{ t('content.abToggleHint') }}</span>
    </span>
  </div>
</template>
