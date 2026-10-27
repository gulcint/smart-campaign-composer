<script setup lang="ts">
// ui.md §3.11 / DESIGN.md §3.4: always icon + text, never colour alone.
import {
  Check,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  Clock,
  Flag,
  Loader,
  PenLine,
  Send,
  Sparkles,
  ThumbsUp,
  Unplug,
  Users,
  X,
} from '@lucide/vue'
import type { Component } from 'vue'
import { computed } from 'vue'

import type { MessageId } from '@/i18n'
import { hasMessage, t } from '@/i18n'
import { cn } from '@/lib/utils'

type Tone = 'neutral' | 'success' | 'warning' | 'error' | 'info' | 'brand'

const props = withDefaults(
  defineProps<{
    kind: 'content' | 'delivery' | 'campaign'
    status: string
    size?: 'sm' | 'md'
  }>(),
  { size: 'md' },
)

const LOOK: Record<string, { tone: Tone; icon: Component }> = {
  'content.draft': { tone: 'neutral', icon: PenLine },
  'content.validated': { tone: 'success', icon: CircleCheck },
  'content.invalid': { tone: 'error', icon: CircleAlert },
  'content.interrupted': { tone: 'warning', icon: Unplug },
  'delivery.queued': { tone: 'neutral', icon: Clock },
  'delivery.sending': { tone: 'info', icon: Loader },
  'delivery.sent': { tone: 'success', icon: Check },
  'delivery.failed': { tone: 'error', icon: X },
  'campaign.draft': { tone: 'neutral', icon: PenLine },
  'campaign.audience_ready': { tone: 'info', icon: Users },
  'campaign.content_ready': { tone: 'info', icon: Sparkles },
  'campaign.approved': { tone: 'success', icon: ThumbsUp },
  'campaign.sending': { tone: 'info', icon: Send },
  'campaign.completed': { tone: 'brand', icon: Flag },
}

const TONE_CLASS: Record<Tone, string> = {
  neutral: 'bg-neutral-status-bg text-neutral-status',
  success: 'bg-success-bg text-success',
  warning: 'bg-warning-bg text-warning',
  error: 'bg-error-bg text-error',
  info: 'bg-info-bg text-info',
  brand: 'bg-brand-status-bg text-brand-status',
}

const key = computed(() => `${props.kind}.${props.status}`)
const look = computed(() => LOOK[key.value] ?? { tone: 'neutral' as Tone, icon: CircleDashed })
const label = computed(() => {
  const id = `status.${key.value}`
  return hasMessage(id) ? t(id as MessageId) : props.status
})
</script>

<template>
  <span
    data-testid="status-badge"
    :data-kind="kind"
    :data-status="status"
    :class="
      cn(
        'inline-flex items-center gap-1 rounded-pill font-medium whitespace-nowrap',
        size === 'sm' ? 'h-5 px-2 text-caption' : 'h-6 px-2.5 text-caption',
        TONE_CLASS[look.tone],
      )
    "
  >
    <component :is="look.icon" class="size-3.5" aria-hidden="true" />
    <span>{{ label }}</span>
  </span>
</template>
