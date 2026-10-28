<script setup lang="ts">
// ui.md §3.22: renders useNotify() toasts. Custom (not vue-sonner) so every toast carries
// the contract attributes (data-variant, data-message-id) and a deterministic close button.
import { CircleAlert, CircleCheck, Info, TriangleAlert, X } from '@lucide/vue'

import type { ToastVariant } from '@/composables/useNotify'
import { useNotify } from '@/composables/useNotify'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const { toasts, dismiss } = useNotify()

const ICON = { success: CircleCheck, info: Info, warning: TriangleAlert, error: CircleAlert }
const TONE: Record<ToastVariant, string> = {
  success: 'text-success',
  info: 'text-info',
  warning: 'text-warning',
  error: 'text-error',
}
// A tinted card per variant, so a confirmation reads at a glance and not as a grey note.
const CARD: Record<ToastVariant, string> = {
  success: 'border-success/40 bg-success-bg',
  info: 'border-info/40 bg-info-bg',
  warning: 'border-warning/40 bg-warning-bg',
  error: 'border-error/40 bg-error-bg',
}
</script>

<template>
  <section
    :aria-label="t('toast.region')"
    class="pointer-events-none fixed inset-x-0 top-20 z-50 flex flex-col items-center gap-2 px-4"
  >
    <TransitionGroup
      enter-from-class="-translate-y-2 opacity-0"
      leave-to-class="opacity-0"
      enter-active-class="transition duration-[var(--dur-base)] ease-standard"
      leave-active-class="transition duration-[var(--dur-fast)]"
    >
      <div
        v-for="toast in toasts"
        :key="toast.id"
        data-testid="toast"
        :data-variant="toast.variant"
        :data-message-id="toast.messageId"
        :role="toast.variant === 'error' ? 'alert' : 'status'"
        :class="
          cn(
            'pointer-events-auto flex w-full max-w-md items-start gap-3 rounded-card border p-4 shadow-raised',
            CARD[toast.variant],
          )
        "
      >
        <component
          :is="ICON[toast.variant]"
          :class="cn('size-6 shrink-0', TONE[toast.variant])"
          aria-hidden="true"
        />
        <p class="flex-1 text-body font-semibold text-ink-900">
          {{ t(toast.messageId, toast.params) }}
        </p>
        <button
          v-if="toast.variant === 'error'"
          type="button"
          data-testid="toast-close"
          :aria-label="t('toast.close')"
          class="-m-1 rounded-pill p-1 text-ink-500 hover:bg-accent hover:text-ink-900"
          @click="dismiss(toast.id)"
        >
          <X class="size-4" aria-hidden="true" />
        </button>
      </div>
    </TransitionGroup>
  </section>
</template>
