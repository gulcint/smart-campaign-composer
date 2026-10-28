<script setup lang="ts">
// ui.md §3.10 (REQ-CNT-04, REQ-UI-01): one streamed field. Text is interpolated only,
// never parsed as HTML.
import { Info, RotateCcw, Unplug } from '@lucide/vue'
import { computed } from 'vue'

import type { ContentPhase, FieldError, FieldKey, LlmProvider } from '@/api/types'
import type { MessageId } from '@/i18n'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

import Button from './Button.vue'
import InlineError from './InlineError.vue'
import Skeleton from './Skeleton.vue'
import StatusBadge from './StatusBadge.vue'

const props = withDefaults(
  defineProps<{
    field: FieldKey
    text: string
    phase: ContentPhase
    errors?: FieldError[]
    provider?: LlmProvider | null
  }>(),
  { errors: () => [], provider: null },
)
const emit = defineEmits<{ regenerate: [] }>()

const LABEL: Record<FieldKey, MessageId> = {
  'sms-body': 'editor.smsBody',
  'email-subject': 'editor.emailSubject',
  'email-body': 'editor.emailBody',
}

const busy = computed(() => props.phase === 'streaming' || props.phase === 'retrying')
// Waiting state: stream open (or being corrected) but no text yet (REQ-CNT-04).
const waiting = computed(() => busy.value && props.text === '')
const badge = computed(() =>
  props.phase === 'streaming' || props.phase === 'retrying' || props.phase === 'idle'
    ? 'draft'
    : props.phase,
)
</script>

<template>
  <section
    :data-testid="`streaming-text-${field}`"
    :data-phase="phase"
    :aria-busy="busy ? 'true' : undefined"
    :aria-label="t(LABEL[field])"
    class="flex flex-col gap-2 rounded-card border border-border-default bg-surface-card p-4"
  >
    <header class="flex items-center justify-between gap-2">
      <h3 class="text-h4 text-accent-heading">{{ t(LABEL[field]) }}</h3>
      <StatusBadge
        :data-testid="`streaming-text-badge-${field}`"
        kind="content"
        :status="badge"
        size="sm"
      />
    </header>
    <p
      v-if="phase === 'retrying'"
      :data-testid="`streaming-text-retrying-${field}`"
      class="flex items-center gap-1.5 text-body-sm text-info"
    >
      <Info class="size-4" aria-hidden="true" />
      {{ t('stream.retrying') }}
    </p>
    <div
      v-if="waiting"
      :data-testid="`streaming-text-waiting-${field}`"
      class="flex flex-col gap-2"
    >
      <p class="text-body-sm text-accent-heading">
        {{ t(provider === 'claude' ? 'stream.waiting.claude' : 'stream.waiting') }}
      </p>
      <div aria-hidden="true" class="flex flex-col gap-2">
        <Skeleton
          v-for="w in ['w-full', 'w-5/6', 'w-2/3']"
          :key="w"
          variant="shimmer"
          :class="['h-3', w]"
        />
      </div>
    </div>
    <p
      v-else
      :class="
        cn(
          'min-h-6 text-body whitespace-pre-wrap break-words text-ink-900',
          phase === 'interrupted' && 'text-ink-500',
        )
      "
    >
      <span :data-testid="`streaming-text-content-${field}`">{{ text }}</span
      ><span
        v-if="phase === 'streaming'"
        :data-testid="`streaming-text-caret-${field}`"
        class="ml-0.5 inline-block h-[1.1em] w-0.5 animate-pulse bg-brand align-text-bottom motion-reduce:animate-none"
        aria-hidden="true"
      />
    </p>
    <div
      v-if="phase === 'interrupted'"
      :data-testid="`streaming-text-interrupted-${field}`"
      class="flex flex-wrap items-center justify-between gap-2 rounded-sm bg-warning-bg px-3 py-2 text-body-sm text-warning"
    >
      <span class="flex items-center gap-1.5">
        <Unplug class="size-4" aria-hidden="true" />
        {{ t('stream.interrupted') }}
      </span>
      <Button
        :data-testid="`streaming-text-regenerate-${field}`"
        variant="secondary"
        size="sm"
        @click="emit('regenerate')"
      >
        <RotateCcw aria-hidden="true" />
        {{ t('stream.regenerate') }}
      </Button>
    </div>
    <ul
      v-if="phase === 'invalid'"
      :data-testid="`streaming-text-errors-${field}`"
      :aria-label="t('stream.errors')"
      class="flex flex-col gap-1"
    >
      <li
        v-for="(e, i) in errors"
        :key="`${e.code}-${i}`"
        data-testid="field-error"
        :data-code="e.code"
      >
        <InlineError
          :path="e.path || field"
          :code="e.code"
          :limit="e.limit ?? null"
          :actual="e.actual ?? null"
        />
      </li>
    </ul>
  </section>
</template>
