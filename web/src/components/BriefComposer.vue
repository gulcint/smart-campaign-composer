<script setup lang="ts">
// ui.md §3.5 (REQ-AUD-05): counter counts code points after NFC + trim, like the API.
import { ArrowUpRight, Lightbulb } from '@lucide/vue'
import { computed, useId } from 'vue'

import type { FieldError } from '@/api/types'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

import Button from './Button.vue'
import InlineError from './InlineError.vue'

const MAX = 2000
const NEAR = 1800

const props = withDefaults(
  defineProps<{
    modelValue: string
    submitting?: boolean
    readonly?: boolean
    fieldErrors?: FieldError[]
  }>(),
  { submitting: false, readonly: false, fieldErrors: () => [] },
)

const emit = defineEmits<{
  'update:modelValue': [value: string]
  submit: [brief: string]
  'use-example': []
}>()

const inputId = useId()
const counterId = useId()

const count = computed(() => [...props.modelValue.normalize('NFC').trim()].length)
const counterState = computed(() =>
  count.value > MAX ? 'over-limit' : count.value >= NEAR ? 'near-limit' : 'normal',
)
const briefErrors = computed(() => props.fieldErrors.filter((e) => e.path === 'brief'))
const canSubmit = computed(
  () => count.value > 0 && count.value <= MAX && !props.submitting && !props.readonly,
)
const describedBy = computed(() =>
  [counterId, ...briefErrors.value.map(() => 'inline-error-brief')].join(' '),
)

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLTextAreaElement).value)
}

function submit() {
  if (!canSubmit.value) return
  emit('submit', props.modelValue)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    submit()
  }
}
</script>

<template>
  <div
    :class="
      cn(
        'rounded-card border bg-surface-card shadow-card transition-shadow',
        'focus-within:border-primary focus-within:shadow-focus',
        counterState === 'over-limit' || briefErrors.length
          ? 'border-error border-[1.5px]'
          : 'border-border-default',
      )
    "
  >
    <label :for="inputId" class="sr-only">{{ t('brief.inputLabel') }}</label>
    <textarea
      :id="inputId"
      data-testid="brief-input"
      :value="modelValue"
      :readonly="readonly || submitting"
      :placeholder="t('brief.placeholder')"
      :aria-describedby="describedBy"
      :aria-invalid="counterState === 'over-limit' || briefErrors.length > 0 ? 'true' : undefined"
      rows="4"
      class="block min-h-32 w-full resize-y rounded-t-card bg-transparent px-5 pt-5 pb-2 text-body-lg text-ink-900 outline-none focus-visible:shadow-none read-only:text-ink-600"
      @input="onInput"
      @keydown="onKeydown"
    />
    <div class="flex flex-wrap items-center justify-between gap-3 px-4 pt-1 pb-4">
      <Button
        data-testid="brief-example"
        variant="secondary"
        size="sm"
        :disabled="readonly || submitting"
        @click="emit('use-example')"
      >
        <Lightbulb aria-hidden="true" />
        {{ t('brief.example') }}
      </Button>
      <div class="ml-auto flex items-center gap-3">
        <span
          :id="counterId"
          data-testid="brief-counter"
          :data-count="count"
          :data-state="counterState"
          :aria-label="t('brief.counterLabel', { n: count, max: MAX })"
          :class="
            cn(
              'text-numeral-sm',
              counterState === 'normal' && 'text-ink-500',
              counterState === 'near-limit' && 'text-warning',
              counterState === 'over-limit' && 'text-error',
            )
          "
          >{{ count }}/{{ MAX }}</span
        >
        <Button
          data-testid="brief-submit"
          :disabled="!canSubmit"
          :loading="submitting"
          :title="t('brief.shortcut')"
          @click="submit"
        >
          {{ t('brief.submit') }}
          <ArrowUpRight aria-hidden="true" />
        </Button>
      </div>
    </div>
    <div v-if="briefErrors.length" class="space-y-1 px-5 pb-4">
      <InlineError
        v-for="(e, i) in briefErrors"
        :key="`${e.code}-${i}`"
        :path="e.path"
        :code="e.code"
        :limit="e.limit ?? null"
        :actual="e.actual ?? null"
      />
    </div>
  </div>
</template>
