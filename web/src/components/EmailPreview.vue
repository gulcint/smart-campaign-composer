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

import BrandMark from './BrandMark.vue'
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
    :data-language="preview?.language"
    :aria-busy="state === 'loading' ? 'true' : undefined"
    class="flex flex-col gap-3"
  >
    <template v-if="shown">
      <!-- Same field chrome as ContentEditor's subject input and body textarea (DESIGN.md). -->
      <div class="flex flex-col gap-1.5">
        <p class="flex items-center gap-1.5 text-body-sm font-semibold text-ink-600">
          <Mail class="size-4 shrink-0 text-email" aria-hidden="true" />
          {{ t('preview.subject') }}
        </p>
        <p
          data-testid="email-preview-subject"
          class="flex min-h-11 items-center rounded-input border border-border-default bg-surface-card px-4 py-2.5 text-body break-words text-ink-900"
        >
          {{ shown.email.subject }}
        </p>
      </div>
      <!-- Brand strip outside the sandbox (srcdoc is a backend contract and cannot load /brand/...). -->
      <div class="flex flex-col">
        <div
          data-testid="email-preview-brand"
          aria-hidden="true"
          class="flex items-center gap-2 rounded-t-input border border-b-0 border-border-default bg-surface-card px-4 py-2 text-body-sm font-semibold text-ink-900"
        >
          <BrandMark :size="20" />
          <span>{{ t('app.wordmark') }}</span>
          <span class="text-accent-display">{{ t('app.wordmarkAccent') }}</span>
        </div>
        <iframe
          data-testid="email-preview-frame"
          sandbox=""
          :srcdoc="shown.email.html"
          :title="t('preview.emailFrameTitle')"
          class="block h-96 w-full rounded-b-input border border-border-default bg-surface-card"
        />
      </div>
    </template>
    <Skeleton v-else-if="state === 'loading'" class="h-96 w-full" />
    <p v-else-if="state === 'empty'" class="text-body-sm text-ink-500">{{ t('preview.empty') }}</p>
    <p v-else-if="error" role="alert" class="text-body-sm text-error">
      {{ t(errorPresentation(error).messageId) }}
    </p>
  </div>
</template>
