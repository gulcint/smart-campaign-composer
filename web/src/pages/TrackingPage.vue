<script setup lang="ts">
// Step 5 (ui.md §1, §3.20, §5.3; DESIGN.md §8.5): live delivery counters over the status SSE
// stream. A dropped stream turns the counters stale and shows the ConnectionBanner; the
// composable reconnects with backoff. `?status=failed` lists the failed deliveries (§3.20 link).
// The ActivityFeed is out of scope by owner decision (counters only).
// P7a (ui.md §3.25): while the campaign is `scheduled` the SchedulePanel (planned time, countdown,
// cancel, send now) sits above the counters; the stream stays open and flips the view live.
import { CalendarClock, X } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { toApiError } from '@/api/errors'
import type { CampaignState, Delivery } from '@/api/types'
import Button from '@/components/Button.vue'
import DeliveryCounters from '@/components/DeliveryCounters.vue'
import EmptyState from '@/components/EmptyState.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorCard from '@/components/ErrorCard.vue'
import SchedulePanel from '@/components/SchedulePanel.vue'
import Skeleton from '@/components/Skeleton.vue'
import StepHeader from '@/components/StepHeader.vue'
import { useCampaignStatusStream } from '@/composables/useCampaignStatusStream'
import { useNotify } from '@/composables/useNotify'
import type { MessageId } from '@/i18n'
import { formatNumber, hasMessage, t } from '@/i18n'
import { formatTsi } from '@/lib/schedule'
import { campaignIdOf } from '@/router'
import { injectApiClient, reportError } from '@/stores/api'
import { useCampaignStore } from '@/stores/campaign'
import { useUiStore } from '@/stores/ui'

const FAILED_LIMIT = 50

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()
const ui = useUiStore()
const client = injectApiClient()
const { notify } = useNotify()

const campaignId = computed(() => (route ? (campaignIdOf(route) ?? '') : ''))
const campaign = computed(() =>
  campaignId.value && store.campaign?.id === campaignId.value ? store.campaign : null,
)
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))

// The stream opens only for a campaign that loaded without error (REQ-TRK-01: an unknown
// campaign creates no EventSource at all). Until then, and for a known 404, the id is empty and
// the stream stays idle; if a reload later reports 404, the stream closes again.
const streamId = computed(() => (campaign.value && !notFound.value ? campaignId.value : ''))
const { status, connection, lastUpdatedAt, completed } = useCampaignStatusStream(streamId)

// A send we started ("send now", or a reload that found the worker already started it) is
// shown at once; the stream catches up with the same state a moment later and clears it.
const started = ref<{ state: CampaignState; scheduledAt: string | null } | null>(null)

// The stream's state is fresher than the loaded campaign (it moves to completed live).
const campaignState = computed<CampaignState | null>(
  () => started.value?.state ?? status.value?.state ?? campaign.value?.state ?? null,
)
const scheduledAt = computed<string | null>(() => {
  if (started.value) return started.value.scheduledAt
  return status.value ? status.value.scheduled_at : (campaign.value?.scheduled_at ?? null)
})
const isScheduled = computed(
  () => campaignState.value === 'scheduled' && scheduledAt.value !== null,
)
// Sending/completed after a plan: say when it was planned for (not for an immediate send).
const startedNote = computed(() =>
  (campaignState.value === 'sending' || campaignState.value === 'completed') && scheduledAt.value
    ? scheduledAt.value
    : null,
)

watch(
  () => status.value?.state,
  (s, prev) => {
    if (s && s !== 'scheduled') started.value = null
    // The header and stepper follow the stream (completion is reloaded below).
    if (
      s &&
      prev !== undefined &&
      s !== 'completed' &&
      s !== campaign.value?.state &&
      campaignId.value
    ) {
      void store.load(campaignId.value)
    }
  },
)

// Cancelled elsewhere (another tab): the approval step is the place to continue.
watch(campaignState, (s) => {
  if (s === 'approved' && route?.name === 'tracking' && campaignId.value) {
    void router.push({ name: 'approval', params: { campaignId: campaignId.value } })
  }
})

// ---- cancel the plan / send now (ui.md §3.25) ----
const cancelling = ref(false)
const sendingNow = ref(false)
const dialogOpen = ref(false)
const dialogError = ref<ApiError | null>(null)
const submitting = ref(false)

const sendCounts = computed(() => {
  const a = campaign.value?.segment?.audience
  return a ? { sms: a.sms, email: a.email } : null
})

// "Send now" stays pending until the stream moves past `scheduled`, or the stream is no longer
// live (then its next snapshot is the truth anyway).
watch([() => status.value?.state, connection], ([s, c]) => {
  if (sendingNow.value && ((s && s !== 'scheduled') || c !== 'open')) sendingNow.value = false
})

/** After a failed request: re-read the campaign; a send the worker already started is shown as such. */
async function resync() {
  const id = campaignId.value
  if (!id) return
  const c = await store.load(id)
  if (c && (c.state === 'sending' || c.state === 'completed')) {
    started.value = { state: c.state, scheduledAt: c.scheduled_at }
  }
}

async function onCancelPlan() {
  const id = campaignId.value
  if (!id || cancelling.value || sendingNow.value) return
  cancelling.value = true
  try {
    await api.cancelSchedule(client, id)
    await store.load(id)
    notify({ variant: 'info', messageId: 'toast.scheduleCanceled' })
    void router.push({ name: 'approval', params: { campaignId: id } })
  } catch (e) {
    const err = toApiError(e)
    reportError(err, 'tracking')
    if (err.status === 409 || err.status === 404) await resync()
  } finally {
    cancelling.value = false
  }
}

function openSendNow() {
  if (cancelling.value || sendingNow.value) return
  dialogError.value = null
  dialogOpen.value = true
}

function closeDialog() {
  if (submitting.value) return
  dialogOpen.value = false
  dialogError.value = null
}

async function onSendNow() {
  const id = campaignId.value
  const versionId = campaign.value?.approval?.content_version_id
  if (!id || !versionId || submitting.value) return
  submitting.value = true
  dialogError.value = null
  try {
    const res = await api.sendCampaign(client, id, { content_version_id: versionId })
    dialogOpen.value = false
    notify({
      variant: 'success',
      messageId: 'toast.sendStarted',
      params: { total: res.deliveries.total },
    })
    if (res.state === 'scheduled') {
      sendingNow.value = true
    } else {
      started.value = { state: res.state, scheduledAt: res.scheduled_at }
    }
    void store.load(id)
  } catch (e) {
    const err = toApiError(e)
    if (err.code === 'validation_failed') {
      dialogError.value = err
    } else {
      dialogOpen.value = false
      reportError(err, 'send')
      if (err.status === 409 || err.status === 404) await resync()
    }
  } finally {
    submitting.value = false
  }
}

// ConnectionBanner (ui.md §4 network_error): only once a live connection was lost, not while
// the first connection is still being made.
const wasOpen = ref(false)
watch(
  [connection, notFound],
  ([c, nf]) => {
    if (c === 'open') wasOpen.value = true
    ui.connectionLost = !nf && (c === 'error' || (c === 'connecting' && wasOpen.value))
  },
  { immediate: true },
)

// Completion moves the campaign to `completed`: reload it so the header and stepper follow.
watch(completed, (done) => {
  if (done && campaignId.value) void store.load(campaignId.value)
})

// ---- failed deliveries (`?status=failed`) ----
const showFailed = computed(() => route?.query.status === 'failed')
const failed = ref<Delivery[] | null>(null)
const failedTotal = ref(0)
const failedError = ref<ApiError | null>(null)
const failedLoading = ref(false)
// Only the newest request may write the list (a campaign switch or a quick reopen must not be
// overwritten by an older answer).
let failedSeq = 0

async function loadFailed() {
  const id = streamId.value
  const seq = ++failedSeq
  if (!id) return
  failedLoading.value = true
  failedError.value = null
  try {
    const page = await api.listDeliveries(client, id, {
      status: 'failed',
      order: 'recent',
      limit: FAILED_LIMIT,
    })
    if (seq !== failedSeq) return
    failed.value = page.items
    failedTotal.value = page.total
  } catch (e) {
    if (seq !== failedSeq) return
    failedError.value = toApiError(e)
  } finally {
    if (seq === failedSeq) failedLoading.value = false
  }
}

watch(
  [showFailed, completed, streamId],
  ([show], [, , prevId]) => {
    if (prevId !== undefined && prevId !== streamId.value) {
      failed.value = null
      failedTotal.value = 0
    }
    if (show) void loadFailed()
    else failedSeq++
  },
  { immediate: true },
)

// Opening the list moves focus to its heading; closing it returns focus to the link (S1).
watch(showFailed, async (show, was) => {
  if (show === was) return
  await nextTick()
  if (show) {
    document.getElementById('delivery-failed-title')?.focus()
    return
  }
  const link = document.querySelector<HTMLElement>('[data-testid="delivery-failed-link"]')
  if (link) link.focus()
  // No link (no failures, or not completed): the page title, without scrolling to the top.
  else
    document
      .querySelector<HTMLElement>('[data-testid="page-title"]')
      ?.focus({ preventScroll: true })
})

function closeFailed() {
  void router.replace({ query: { ...route.query, status: undefined } })
}

function reasonText(reason: string | null): string {
  if (!reason) return '—'
  const id = `delivery.failureReason.${reason}`
  return hasMessage(id) ? t(id as MessageId) : reason
}

onMounted(() => {
  if (campaignId.value && !notFound.value) void store.load(campaignId.value)
})

onBeforeUnmount(() => {
  ui.connectionLost = false
})
</script>

<template>
  <div data-testid="page-tracking" class="mx-auto flex w-full max-w-5xl flex-col gap-6">
    <EmptyState
      v-if="notFound"
      kind="campaign_not_found"
      @action="router.push({ name: 'brief-new' })"
    />
    <template v-else>
      <StepHeader
        step="tracking"
        :role="t('tracking.role')"
        :title="t('tracking.title')"
        :subtitle="t('tracking.subtitle')"
        status-kind="campaign"
        :status="campaignState"
        :next="null"
      />

      <SchedulePanel
        v-if="isScheduled && scheduledAt"
        :scheduled-at="scheduledAt"
        :cancelling="cancelling"
        :sending-now="sendingNow"
        @cancel="onCancelPlan"
        @send-now="openSendNow"
      />

      <p
        v-if="startedNote"
        data-testid="schedule-started-note"
        :data-value="startedNote"
        class="flex items-center gap-2 text-body-sm text-ink-700"
      >
        <CalendarClock class="size-4 shrink-0 text-info" aria-hidden="true" />
        {{ t('schedule.startedNote', { time: formatTsi(startedNote) }) }}
      </p>

      <DeliveryCounters
        :status="status"
        :connection="connection"
        :last-updated-at="lastUpdatedAt"
        :view-state="campaignState"
      />

      <ConfirmDialog
        :open="dialogOpen"
        kind="send"
        :counts="sendCounts"
        :submitting="submitting"
        :error="dialogError"
        @confirm="onSendNow"
        @cancel="closeDialog"
        @update:open="(v) => !v && closeDialog()"
      />

      <section
        v-if="showFailed"
        data-testid="delivery-failed-list"
        aria-labelledby="delivery-failed-title"
        class="flex flex-col gap-3 rounded-card border border-border-default bg-surface-card p-4 md:p-6"
      >
        <div class="flex items-center justify-between gap-3">
          <h2
            id="delivery-failed-title"
            tabindex="-1"
            class="text-h4 text-accent-heading outline-none"
          >
            {{ t('delivery.failedList.title') }}
          </h2>
          <Button variant="ghost" size="sm" @click="closeFailed">
            <X aria-hidden="true" />
            {{ t('delivery.failedList.close') }}
          </Button>
        </div>

        <ErrorCard v-if="failedError" :error="failedError" action="retry" @action="loadFailed" />
        <div v-else-if="failedLoading && !failed" class="flex flex-col gap-2">
          <Skeleton class="h-6 w-full" />
          <Skeleton class="h-6 w-4/5" />
        </div>
        <p v-else-if="failed && failed.length === 0" class="text-body-sm text-ink-600">
          {{ t('delivery.failedList.empty') }}
        </p>
        <template v-else-if="failed">
          <!-- Below 640 px each row is a two-line card (customer + channel, reason + attempts)
               instead of a table that would scroll sideways (DESIGN.md §8.6). -->
          <div class="sm:overflow-x-auto">
            <table class="w-full text-left text-body-sm sm:min-w-[28rem]">
              <thead class="text-caption text-ink-500 max-sm:sr-only">
                <tr class="border-b border-border-subtle">
                  <th scope="col" class="py-2 pr-3 font-medium">
                    {{ t('delivery.failedList.customer') }}
                  </th>
                  <th scope="col" class="py-2 pr-3 font-medium">
                    {{ t('delivery.failedList.channel') }}
                  </th>
                  <th scope="col" class="py-2 pr-3 text-right font-medium">
                    {{ t('delivery.failedList.attempts') }}
                  </th>
                  <th scope="col" class="py-2 font-medium">
                    {{ t('delivery.failedList.reason') }}
                  </th>
                </tr>
              </thead>
              <tbody class="text-ink-700">
                <tr
                  v-for="d in failed"
                  :key="d.id"
                  data-testid="delivery-failed-item"
                  :data-delivery-id="d.id"
                  :data-channel="d.channel"
                  :data-reason="d.failure_reason ?? undefined"
                  class="border-b border-border-subtle last:border-b-0 max-sm:grid max-sm:grid-cols-[minmax(0,1fr)_auto] max-sm:gap-x-3 max-sm:gap-y-1 max-sm:py-3"
                >
                  <td class="py-2 pr-3 font-mono text-caption break-all max-sm:p-0">
                    {{ d.customer_id }}
                  </td>
                  <td class="py-2 pr-3 max-sm:p-0 max-sm:text-right">
                    <span class="inline-flex items-center gap-1.5">
                      <span
                        :class="[
                          'size-2 rounded-full',
                          d.channel === 'sms' ? 'bg-sms' : 'bg-email',
                        ]"
                        aria-hidden="true"
                      />
                      {{ d.channel === 'sms' ? t('tabs.sms') : t('tabs.email') }}
                    </span>
                  </td>
                  <td class="py-2 pr-3 text-right tabular-nums max-sm:order-last max-sm:p-0">
                    <span class="sm:hidden">{{ t('delivery.failedList.attempts') }}: </span
                    >{{ formatNumber(d.attempts) }}
                  </td>
                  <td class="py-2 max-sm:p-0 max-sm:text-ink-600">
                    {{ reasonText(d.failure_reason) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="failedTotal > failed.length" class="text-caption text-ink-500">
            {{
              t('delivery.failedList.more', {
                shown: formatNumber(failed.length),
                total: formatNumber(failedTotal),
              })
            }}
          </p>
        </template>
      </section>
    </template>
  </div>
</template>
