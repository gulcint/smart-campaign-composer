<script setup lang="ts">
// ui.md §3.18: confirmation for approve / send / regenerate. Reka AlertDialog gives the
// focus trap, Esc handling, focus return and aria-labelledby; rendered in place (no portal)
// so the dialog stays inside the owning page's DOM. While submitting, confirm is inert and
// cancel / Esc / backdrop do not close it.
import {
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogRoot,
  AlertDialogTitle,
} from 'reka-ui'

import type { ApiError } from '@/api/errors'
import { errorPresentation } from '@/api/errors'
import { formatNumber, t } from '@/i18n'

import Button from './Button.vue'
import InlineError from './InlineError.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    kind: 'approve' | 'send' | 'regenerate'
    counts?: { sms: number; email: number } | null
    submitting?: boolean
    error?: ApiError | null
  }>(),
  { counts: null, submitting: false, error: null },
)
const emit = defineEmits<{ confirm: []; cancel: []; 'update:open': [value: boolean] }>()

function onOpenChange(v: boolean) {
  if (!v) cancel()
}

function onConfirm() {
  if (!props.submitting) emit('confirm')
}

function cancel() {
  if (props.submitting) return
  emit('cancel')
  emit('update:open', false)
}
</script>

<template>
  <AlertDialogRoot :open="open" @update:open="onOpenChange">
    <AlertDialogOverlay class="fixed inset-0 z-40 bg-ink-900/30" />
    <AlertDialogContent
      data-testid="confirm-dialog"
      :data-kind="kind"
      aria-modal="true"
      class="fixed top-1/2 left-1/2 z-50 flex w-[calc(100%-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 flex-col gap-4 rounded-card-lg bg-surface-raised p-6 shadow-raised"
    >
      <AlertDialogTitle data-testid="confirm-dialog-title" class="text-h3 text-accent-heading">
        {{ t(`confirm.${kind}.title`) }}
      </AlertDialogTitle>
      <AlertDialogDescription class="text-body text-ink-700">
        {{ t(`confirm.${kind}.body`) }}
      </AlertDialogDescription>
      <p v-if="kind === 'send' && counts" class="text-body text-ink-700">
        {{ t('audience.channelSms') }}
        <span
          data-testid="confirm-dialog-count-sms"
          :data-value="counts.sms"
          class="font-semibold text-accent-numeral tabular-nums"
          >{{ formatNumber(counts.sms) }}</span
        >
        · {{ t('audience.channelEmail') }}
        <span
          data-testid="confirm-dialog-count-email"
          :data-value="counts.email"
          class="font-semibold text-accent-numeral tabular-nums"
          >{{ formatNumber(counts.email) }}</span
        >
      </p>
      <div
        v-if="error"
        data-testid="confirm-dialog-error"
        :data-code="error.code"
        role="alert"
        class="flex flex-col gap-1 rounded-sm bg-error-bg p-3 text-body-sm text-error"
      >
        <p>{{ t(errorPresentation(error, 'approval').messageId) }}</p>
        <div v-for="(f, i) in error.fields" :key="i" data-testid="field-error" :data-code="f.code">
          <InlineError
            :path="f.path"
            :code="f.code"
            :limit="f.limit ?? null"
            :actual="f.actual ?? null"
          />
        </div>
      </div>
      <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button data-testid="confirm-dialog-cancel" variant="ghost" @click="cancel">
          {{ t('confirm.cancel') }}
        </Button>
        <Button
          data-testid="confirm-dialog-confirm"
          :disabled="submitting"
          :loading="submitting"
          @click="onConfirm"
        >
          {{ t('confirm.confirm') }}
        </Button>
      </div>
    </AlertDialogContent>
  </AlertDialogRoot>
</template>
