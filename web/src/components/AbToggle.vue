<script setup lang="ts">
// ui.md §3.29 (REQ-ABT-04): the "A/B testi" switch next to generate / regenerate. It only
// shapes the next generation request ({"ab":true}); the current version is left alone.
// Hand-rolled switch (WAI-ARIA `switch`) for the same reason as Button: disabled keeps it
// focusable via aria-disabled (no `disabled` attribute) and a click then changes nothing.
// The page's testid lands on the switch itself (inheritAttrs off, $attrs bound to the button).
// The explanation sits behind an info icon (Reka Tooltip: hover, keyboard focus, tap); a
// visually hidden copy stays in the DOM so the switch's aria-describedby always resolves.
import { FlaskConical, Info } from '@lucide/vue'
import {
  TooltipArrow,
  TooltipContent,
  TooltipPortal,
  TooltipProvider,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'
import { ref, useId } from 'vue'

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

// Touch has no hover: a tap on the icon opens the tooltip; outside tap / Esc closes it.
const infoOpen = ref(false)

function toggle(e: MouseEvent) {
  if (props.disabled) {
    e.preventDefault()
    return
  }
  emit('update:modelValue', !props.modelValue)
}
</script>

<template>
  <div class="flex min-w-0 items-center gap-3">
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
          'group relative inline-flex h-6 w-11 shrink-0 items-center rounded-pill border-2 border-transparent',
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
    <span
      :id="labelId"
      :class="
        cn(
          'text-body-sm font-semibold whitespace-nowrap',
          disabled ? 'text-ink-500' : 'text-ink-900',
        )
      "
      >{{ t('content.abToggle') }}</span
    >
    <span :id="hintId" class="sr-only">{{ t('content.abToggleHint') }}</span>
    <TooltipProvider :delay-duration="150">
      <TooltipRoot v-model:open="infoOpen" disable-closing-trigger>
        <TooltipTrigger
          type="button"
          :aria-label="t('content.abToggleInfo')"
          class="-ml-1.5 inline-flex size-7 shrink-0 cursor-help items-center justify-center rounded-full text-ink-500 outline-none transition-colors duration-150 hover:text-accent-text focus-visible:text-accent-text focus-visible:shadow-focus data-[state=delayed-open]:text-accent-text data-[state=instant-open]:text-accent-text motion-reduce:transition-none max-lg:size-11 max-lg:-ml-3"
          @click="infoOpen = true"
        >
          <Info class="size-4" aria-hidden="true" />
        </TooltipTrigger>
        <TooltipPortal>
          <TooltipContent
            side="top"
            :side-offset="6"
            :collision-padding="16"
            class="z-50 max-w-[min(18rem,calc(100vw-2rem))] rounded-sm bg-ink-900 px-3 py-2 text-caption text-surface-card shadow-raised data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=delayed-open]:animate-in data-[state=delayed-open]:fade-in-0 data-[state=instant-open]:animate-in data-[state=instant-open]:fade-in-0 motion-reduce:animate-none"
          >
            {{ t('content.abToggleHint') }}
            <TooltipArrow class="fill-ink-900" :width="10" :height="5" />
          </TooltipContent>
        </TooltipPortal>
      </TooltipRoot>
    </TooltipProvider>
  </div>
</template>
