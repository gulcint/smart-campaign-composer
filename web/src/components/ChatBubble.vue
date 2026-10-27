<script setup lang="ts">
// ui.md §3.6: user text is shown with text interpolation only (REQ-UI-01).
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

withDefaults(defineProps<{ role: 'user' | 'assistant'; text: string; pending?: boolean }>(), {
  pending: false,
})
</script>

<template>
  <div
    data-testid="chat-bubble"
    :data-role="role"
    :class="cn('flex w-full gap-2', role === 'user' ? 'justify-end' : 'justify-start')"
  >
    <!-- Brand glyph: three short bars (DESIGN.md §7), not a Claude logo -->
    <span
      v-if="role === 'assistant'"
      class="mt-2 flex size-5 shrink-0 flex-col justify-center gap-[3px]"
      aria-hidden="true"
    >
      <span class="h-[3px] w-5 rounded-pill bg-gradient-brand" />
      <span class="h-[3px] w-3.5 rounded-pill bg-gradient-brand opacity-70" />
      <span class="h-[3px] w-2 rounded-pill bg-gradient-brand opacity-40" />
    </span>
    <div
      :class="
        cn(
          'max-w-[min(42rem,85%)] rounded-bubble px-4 py-3 text-body',
          role === 'user'
            ? 'rounded-br-[4px] bg-bubble-user text-ink-600'
            : 'border border-border-subtle bg-bubble-ai text-ink-700',
        )
      "
    >
      <span class="sr-only">{{ role === 'user' ? t('chat.user') : t('chat.assistant') }}:</span>
      <p data-testid="chat-bubble-text" class="whitespace-pre-wrap break-words">{{ text }}</p>
      <span
        v-if="pending"
        data-testid="chat-bubble-pending"
        role="status"
        class="mt-1 flex items-center gap-1"
      >
        <span class="sr-only">{{ t('chat.pending') }}</span>
        <span class="size-1.5 animate-pulse rounded-full bg-ink-300" aria-hidden="true" />
        <span
          class="size-1.5 animate-pulse rounded-full bg-ink-300 [animation-delay:150ms]"
          aria-hidden="true"
        />
        <span
          class="size-1.5 animate-pulse rounded-full bg-ink-300 [animation-delay:300ms]"
          aria-hidden="true"
        />
      </span>
    </div>
  </div>
</template>
