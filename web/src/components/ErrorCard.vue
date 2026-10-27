<script setup lang="ts">
// ui.md §3.22: section-level error; message from errorPresentation(), never from the API text.
import { CircleAlert, RotateCcw } from '@lucide/vue'
import { computed } from 'vue'

import type { ApiError, ErrorContext } from '@/api/errors'
import { errorPresentation } from '@/api/errors'
import { t } from '@/i18n'

import Button from './Button.vue'

const props = withDefaults(
  defineProps<{
    error: ApiError
    action: 'retry' | 'regenerate' | 'new-campaign' | null
    context?: ErrorContext | null
  }>(),
  { context: null },
)
const emit = defineEmits<{ action: [] }>()

const messageId = computed(
  () => errorPresentation(props.error, props.context ?? undefined).messageId,
)
</script>

<template>
  <div
    data-testid="error-card"
    :data-code="error.code"
    role="alert"
    class="flex flex-col gap-3 rounded-card border border-error/30 bg-error-bg/60 p-4 sm:flex-row sm:items-center sm:justify-between"
  >
    <div class="flex items-start gap-3">
      <CircleAlert class="mt-0.5 size-5 shrink-0 text-error" aria-hidden="true" />
      <div>
        <p class="text-h4 text-error-strong">{{ t('errorCard.title') }}</p>
        <p data-testid="error-card-message" class="text-body-sm text-ink-700">
          {{ t(messageId) }}
        </p>
        <p v-if="error.requestId" class="mt-1 font-mono text-caption text-ink-500">
          {{ t('errorCard.requestId', { id: error.requestId }) }}
        </p>
      </div>
    </div>
    <Button
      v-if="action"
      data-testid="error-card-action"
      variant="secondary"
      size="sm"
      @click="emit('action')"
    >
      <RotateCcw v-if="action !== 'new-campaign'" aria-hidden="true" />
      {{ t(`errorCard.action.${action}`) }}
    </Button>
  </div>
</template>
