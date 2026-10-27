<script setup lang="ts">
// ui.md §3.3 (REQ-AI-03): shows which LLM provider the backend selected.
import { CircleHelp, FlaskConical, Sparkles } from '@lucide/vue'
import { computed } from 'vue'

import type { LlmProvider } from '@/api/types'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const props = withDefaults(defineProps<{ provider: LlmProvider | null; loading?: boolean }>(), {
  loading: false,
})

const key = computed(() => props.provider ?? 'unknown')
const label = computed(() =>
  key.value === 'mock'
    ? t('provider.mock')
    : key.value === 'claude'
      ? t('provider.claude')
      : t('provider.unknown'),
)
const icon = computed(() =>
  key.value === 'mock' ? FlaskConical : key.value === 'claude' ? Sparkles : CircleHelp,
)
</script>

<template>
  <span
    data-testid="provider-badge"
    :data-provider="key"
    :data-state="loading ? 'loading' : 'ready'"
    :title="t('provider.label')"
    :aria-busy="loading ? 'true' : undefined"
    :class="
      cn(
        'inline-flex h-6 items-center gap-1 rounded-pill px-2.5 text-caption whitespace-nowrap',
        loading
          ? 'w-[88px] animate-pulse bg-track'
          : key === 'mock'
            ? 'bg-warning-bg text-warning'
            : 'border border-border-default bg-surface-card text-ink-600',
      )
    "
  >
    <template v-if="loading">
      <span class="sr-only">{{ label }}</span>
    </template>
    <template v-else>
      <component :is="icon" class="size-3.5" aria-hidden="true" />
      <span>{{ label }}</span>
    </template>
  </span>
</template>
