<script setup lang="ts">
// ui.md §3.17 (REQ-APR-01, -03, REQ-CNT-04, -05): approve / send decision panel. The first
// matching state wins: approved › stale › submitting › ready › disabled (with a visible reason).
// It does not position itself: ApprovalPage pins its wrapper inside the summary card (DESIGN.md §8.4).
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
  <!-- One row from 640 px (note left, actions right); below it the note sits above full-width
       actions with the primary button first. The default slot holds secondary actions. -->
  <div
    data-testid="approval-bar"
    :data-state="state"
    class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between"
  >
    <p
      v-if="state === 'stale'"
      data-testid="approval-stale-note"
      role="status"
      class="flex items-center gap-2 text-body-sm font-semibold text-warning"
    >
      <TriangleAlert class="size-5 shrink-0" aria-hidden="true" />
      {{ t('approval.staleNote') }}
    </p>
    <p
      v-else-if="state === 'disabled'"
      :id="reasonId"
      data-testid="approval-reason"
      :data-reason="reason"
      class="flex items-center gap-2 text-body-sm text-ink-600"
    >
      <Info class="size-4 shrink-0" aria-hidden="true" />
      {{ t(`approval.reason.${reason}`) }}
    </p>
    <p v-else-if="state === 'approved'" class="flex items-center gap-2 text-body-sm text-ink-700">
      <CircleCheck class="size-5 shrink-0 text-success" aria-hidden="true" />
      <span
        ><span class="font-semibold text-success">{{ t('status.campaign.approved') }}</span> ·
        {{ t('approval.approvedNote') }}</span
      >
    </p>
    <p v-else class="flex items-center gap-2 text-body-sm text-ink-600">
      <Info class="size-4 shrink-0 text-ink-500" aria-hidden="true" />
      {{ t('approval.readyNote') }}
    </p>

    <div class="flex flex-col-reverse gap-2 sm:flex-row sm:items-center sm:gap-3">
      <slot />
      <Button
        v-if="state === 'approved'"
        data-testid="approval-send"
        size="lg"
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
        :disabled="state === 'disabled'"
        :loading="state === 'submitting'"
        :aria-describedby="state === 'disabled' ? reasonId : undefined"
        @click="onApprove"
      >
        {{ t('approval.approve') }}
        <ArrowUpRight aria-hidden="true" />
      </Button>
    </div>
  </div>
</template>
