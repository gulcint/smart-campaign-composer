<script setup lang="ts">
// ui.md §3.17 (REQ-APR-01, -03, REQ-CNT-04, -05): approve / send bar. The first matching
// state wins: approved › stale › submitting › ready › disabled (with a visible reason).
import { ArrowUpRight, CircleCheck, Info, Send, TriangleAlert } from '@lucide/vue'
import { computed, useId } from 'vue'

import type { CampaignState, VersionStatus } from '@/api/types'
import { t } from '@/i18n'

import Button from './Button.vue'

type Reason = 'no_content' | 'streaming' | 'invalid' | 'interrupted' | 'dirty' | 'state'

const props = withDefaults(
  defineProps<{
    campaignState: CampaignState
    versionStatus: VersionStatus
    dirty: boolean
    submitting?: boolean
    stale?: boolean
  }>(),
  { submitting: false, stale: false },
)
const emit = defineEmits<{ approve: []; send: [] }>()

const reasonId = useId()

const state = computed(() => {
  if (props.campaignState === 'approved' && !props.stale) return 'approved'
  if (props.stale) return 'stale'
  if (props.submitting) return 'submitting'
  if (
    props.versionStatus === 'validated' &&
    !props.dirty &&
    props.campaignState === 'content_ready'
  ) {
    return 'ready'
  }
  return 'disabled'
})

const reason = computed<Reason>(() => {
  switch (props.versionStatus) {
    case 'none':
      return 'no_content'
    case 'streaming':
    case 'invalid':
    case 'interrupted':
      return props.versionStatus
    default:
      return props.dirty ? 'dirty' : 'state'
  }
})

function onApprove() {
  if (state.value === 'ready' || state.value === 'stale') emit('approve')
}
</script>

<template>
  <div
    data-testid="approval-bar"
    :data-state="state"
    class="sticky bottom-0 z-10 flex flex-col gap-3 rounded-card border border-border-default bg-surface-raised p-4 shadow-raised sm:flex-row sm:items-center sm:justify-between"
  >
    <!-- approved: no note; the step header badge already says "Onaylandı". -->
    <p
      v-if="state === 'stale'"
      data-testid="approval-stale-note"
      role="status"
      class="flex items-center gap-1.5 text-body-sm font-semibold text-warning"
    >
      <TriangleAlert class="size-5" aria-hidden="true" />
      {{ t('approval.staleNote') }}
    </p>
    <p
      v-else-if="state === 'disabled'"
      :id="reasonId"
      data-testid="approval-reason"
      :data-reason="reason"
      class="flex items-center gap-1.5 text-body-sm text-ink-600"
    >
      <Info class="size-4" aria-hidden="true" />
      {{ t(`approval.reason.${reason}`) }}
    </p>
    <p v-else-if="state !== 'approved'" class="flex items-center gap-1.5 text-body-sm text-ink-600">
      <CircleCheck class="size-4 text-success" aria-hidden="true" />
      {{ t('approval.readyNote') }}
    </p>

    <Button
      v-if="state === 'approved'"
      data-testid="approval-send"
      size="lg"
      class="sm:ml-auto"
      :loading="submitting"
      @click="emit('send')"
    >
      <Send aria-hidden="true" />
      {{ t('approval.send') }}
    </Button>
    <Button
      v-else
      data-testid="approval-approve"
      size="lg"
      class="sm:ml-auto"
      :disabled="state === 'disabled'"
      :loading="state === 'submitting'"
      :aria-describedby="state === 'disabled' ? reasonId : undefined"
      @click="onApprove"
    >
      {{ t('approval.approve') }}
      <ArrowUpRight aria-hidden="true" />
    </Button>
  </div>
</template>
