<script setup lang="ts">
// ui.md §3.15 (REQ-APR-01, REQ-CNT-03, REQ-UI-01): the API-rendered SMS for one sample
// recipient, shown as text in a phone bubble. Never HTML.
import { ChevronRight, MessageSquare } from '@lucide/vue'
import { computed } from 'vue'

import type { ApiError } from '@/api/errors'
import { errorPresentation } from '@/api/errors'
import type { Preview } from '@/api/types'
import { t } from '@/i18n'
import { walkedSamples } from '@/lib/preview'

import Button from './Button.vue'
import Skeleton from './Skeleton.vue'

const props = defineProps<{
  preview: Preview | null
  loading: boolean
  error: ApiError | null
}>()
const emit = defineEmits<{ 'next-sample': [] }>()

const state = computed(() => {
  if (props.loading) return 'loading'
  if (props.error) return 'error'
  return props.preview ? 'ready' : 'empty'
})
const shown = computed(() =>
  state.value === 'ready' || state.value === 'loading' ? props.preview : null,
)
const customerMissing = computed(() => props.error?.code === 'customer_not_found')
const showNext = computed(
  () => shown.value !== null || (state.value === 'error' && customerMissing.value),
)
const nextDisabled = computed(() => {
  if (state.value === 'error') return !customerMissing.value
  return (shown.value?.sample_count ?? 0) <= 1
})
const recipient = computed(() => {
  const name = shown.value?.customer.first_name ?? ''
  return name ? name : t('preview.fallbackRecipient')
})

function onNext() {
  if (!nextDisabled.value) emit('next-sample')
}
</script>

<template>
  <div
    data-testid="sms-preview"
    :data-state="state"
    :data-language="preview?.language"
    :aria-busy="state === 'loading' ? 'true' : undefined"
    class="flex flex-col gap-3"
  >
    <template v-if="shown">
      <p class="flex items-center gap-1.5 text-body-sm text-ink-600">
        <MessageSquare class="size-4 text-sms" aria-hidden="true" />
        {{ t('preview.recipient') }}:
        <span data-testid="sms-preview-recipient" class="font-semibold text-ink-900">{{
          recipient
        }}</span>
      </p>
      <div class="rounded-card-lg bg-surface-sunken p-4">
        <p
          data-testid="sms-preview-text"
          class="max-w-[85%] rounded-bubble rounded-bl-sm bg-surface-card px-4 py-3 text-body whitespace-pre-wrap break-words text-ink-900 shadow-card"
        >
          {{ shown.sms.text }}
        </p>
      </div>
    </template>
    <Skeleton v-else-if="state === 'loading'" class="h-24 w-full" />
    <p v-else-if="state === 'empty'" class="text-body-sm text-ink-500">{{ t('preview.empty') }}</p>
    <p v-else-if="error" role="alert" class="text-body-sm text-error">
      {{ t(errorPresentation(error).messageId) }}
    </p>
    <div v-if="showNext" class="flex items-center justify-between gap-2">
      <span v-if="shown" data-testid="sms-preview-sample" class="text-caption text-ink-500">{{
        t('preview.sample', { i: shown.sample_index + 1, n: walkedSamples(shown.sample_count) })
      }}</span>
      <Button
        data-testid="sms-preview-next"
        variant="ghost"
        size="sm"
        :disabled="nextDisabled"
        class="ml-auto"
        @click="onNext"
      >
        {{ t('preview.next') }}
        <ChevronRight aria-hidden="true" />
      </Button>
    </div>
  </div>
</template>
