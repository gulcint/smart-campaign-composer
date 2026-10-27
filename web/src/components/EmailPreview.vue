<script setup lang="ts">
// ui.md §3.16, §6.3 (REQ-APR-01, REQ-UI-01): the subject as text and the backend-rendered,
// escaped HTML in a fully sandboxed iframe (`sandbox=""`, `srcdoc` verbatim, no `src`).
// The client never builds, merges or sanitises email HTML.
import { Mail } from '@lucide/vue'
import { computed } from 'vue'

import type { ApiError } from '@/api/errors'
import { errorPresentation } from '@/api/errors'
import type { Preview } from '@/api/types'
import { t } from '@/i18n'

import Skeleton from './Skeleton.vue'

const props = defineProps<{
  preview: Preview | null
  loading: boolean
  error: ApiError | null
}>()

const state = computed(() => {
  if (props.loading) return 'loading'
  if (props.error) return 'error'
  return props.preview ? 'ready' : 'empty'
})
const shown = computed(() =>
  state.value === 'ready' || state.value === 'loading' ? props.preview : null,
)
</script>

<template>
  <div
    data-testid="email-preview"
    :data-state="state"
    :aria-busy="state === 'loading' ? 'true' : undefined"
    class="flex flex-col gap-3"
  >
    <template v-if="shown">
      <p class="flex items-start gap-1.5 text-body-sm text-ink-600">
        <Mail class="mt-0.5 size-4 shrink-0 text-email" aria-hidden="true" />
        <span>
          {{ t('preview.subject') }}:
          <span data-testid="email-preview-subject" class="font-semibold text-ink-900">{{
            shown.email.subject
          }}</span>
        </span>
      </p>
      <iframe
        data-testid="email-preview-frame"
        sandbox=""
        :srcdoc="shown.email.html"
        :title="t('preview.emailFrameTitle')"
        class="h-96 w-full rounded-card border border-border-default bg-surface-card"
      />
    </template>
    <Skeleton v-else-if="state === 'loading'" class="h-96 w-full" />
    <p v-else-if="state === 'empty'" class="text-body-sm text-ink-500">{{ t('preview.empty') }}</p>
    <p v-else-if="error" role="alert" class="text-body-sm text-error">
      {{ t(errorPresentation(error).messageId) }}
    </p>
  </div>
</template>
