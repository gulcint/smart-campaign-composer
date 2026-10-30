<script setup lang="ts">
// Step 4 (ui.md §3.24, §3.17, §3.18; DECISIONS D45; DESIGN.md §8.4): a read-only send summary
// with one decision panel. Always approves and sends the campaign's latest content version;
// `stale` appears only after a 409. No editor UI and no sample navigation here: that is step 3.
import { CircleAlert, CircleCheck, FileText, PencilLine, RotateCw, Users } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { errorPresentation, toApiError } from '@/api/errors'
import type { Preview } from '@/api/types'
import ApprovalBar from '@/components/ApprovalBar.vue'
import Button from '@/components/Button.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import Skeleton from '@/components/Skeleton.vue'
import StepHeader from '@/components/StepHeader.vue'
import { useNotify } from '@/composables/useNotify'
import { formatNumber, t } from '@/i18n'
import { formatTsi } from '@/lib/schedule'
import { isLockedForEditing } from '@/lib/steps'
import { runeCount } from '@/lib/text-cap'
import { cn } from '@/lib/utils'
import { campaignIdOf } from '@/router'
import { injectApiClient, reportError } from '@/stores/api'
import { useCampaignStore } from '@/stores/campaign'
import { useConfigStore } from '@/stores/config'

const SMS_LIMIT = 160
const SUBJECT_LIMIT = 80

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()
const config = useConfigStore()
const client = injectApiClient()
const { notify } = useNotify()

const campaignId = computed(() => (route ? campaignIdOf(route) : null))
const campaign = computed(() =>
  campaignId.value && store.campaign?.id === campaignId.value ? store.campaign : null,
)
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))
const latest = computed(() => campaign.value?.latest_content_version ?? null)
const audience = computed(() => campaign.value?.segment?.audience ?? null)
const locked = computed(() => isLockedForEditing(campaign.value?.state ?? null))

// REQ-CNT-05: one pass/fail signal from the API's worst case after personalisation (every
// variable filled with the audience's longest value). worst_case is null only for an invalid
// template, which never reaches this page; the template length is the fallback.
const limitCheck = computed(() => {
  const v = latest.value
  if (!v) return null
  const sms = v.worst_case?.sms_runes ?? runeCount(v.content.sms.body)
  const subject = v.worst_case?.subject_runes ?? runeCount(v.content.email.subject)
  const en = v.worst_case?.en
  const enPass = !en || (en.sms_runes <= SMS_LIMIT && en.subject_runes <= SUBJECT_LIMIT)
  return { sms, subject, pass: sms <= SMS_LIMIT && subject <= SUBJECT_LIMIT && enPass }
})

// ---- preview of one sample recipient (ui.md §1.1, REQ-APR-01) ----
const preview = ref<Preview | null>(null)
const previewLoading = ref(false)
const previewError = ref<ApiError | null>(null)
let previewSeq = 0

const previewState = computed(() => {
  if (previewLoading.value) return 'loading'
  if (previewError.value) return 'error'
  return preview.value ? 'ready' : 'empty'
})
const sampleName = computed(
  () => preview.value?.customer.first_name || t('preview.fallbackRecipient'),
)

async function loadPreview() {
  const id = campaignId.value
  if (!id) return
  const seq = ++previewSeq
  previewLoading.value = true
  try {
    const p = await api.previewContent(client, id, 0)
    if (seq !== previewSeq) return
    preview.value = p
    previewError.value = null
  } catch (e) {
    if (seq !== previewSeq) return
    const err = toApiError(e)
    previewError.value = err
    if (err.code === 'customer_not_found' || err.code === 'network_error') reportError(err)
  } finally {
    if (seq === previewSeq) previewLoading.value = false
  }
}

// ---- approve / send (ui.md §3.24) ----
const dialogKind = ref<'approve' | 'send'>('approve')
const dialogOpen = ref(false)
const dialogError = ref<ApiError | null>(null)
const submitting = ref(false)
const stale = ref(false)

// Window for the planning dialog; the API enforces it (GET /api/config limits, ui.md §3.24).
const scheduleMinLead = computed(() => config.config?.limits.schedule_min_lead_seconds ?? 60)
const scheduleMaxAhead = computed(() => config.config?.limits.schedule_max_ahead_seconds ?? 2592000)

const sendCounts = computed(() =>
  audience.value ? { sms: audience.value.sms, email: audience.value.email } : null,
)

function openDialog(kind: 'approve' | 'send') {
  if (kind === 'approve') stale.value = false
  dialogKind.value = kind
  dialogError.value = null
  dialogOpen.value = true
}

function closeDialog() {
  if (submitting.value) return
  dialogOpen.value = false
  dialogError.value = null
}

async function refetch() {
  const id = campaignId.value
  if (id) await store.load(id)
}

async function onRequestError(err: ApiError) {
  if (err.code === 'validation_failed') {
    // 422 (e.g. sms_too_long_worst_case): the dialog stays open and lists the errors.
    dialogError.value = err
    return
  }
  dialogOpen.value = false
  dialogError.value = null
  if (err.code === 'stale_content_version') stale.value = true
  reportError(err, 'approval')
  if (err.status === 409 || err.status === 404) await refetch()
}

async function onConfirm() {
  const id = campaignId.value
  const c = campaign.value
  if (!id || !c || submitting.value) return
  submitting.value = true
  dialogError.value = null
  try {
    if (dialogKind.value === 'approve') {
      const v = latest.value
      if (!v) return
      store.campaign = await api.approveContent(client, id, { content_version_id: v.id })
      stale.value = false
      dialogOpen.value = false
      notify({ variant: 'success', messageId: 'toast.approved' })
    } else {
      await send(id, c.approval?.content_version_id ?? latest.value?.id)
    }
  } catch (e) {
    await onRequestError(toApiError(e))
  } finally {
    submitting.value = false
  }
}

/** POST /send, immediate or (with `scheduledAt`, UTC ISO) planned; both end on the tracking page. */
async function send(id: string, versionId: string | undefined, scheduledAt?: string) {
  if (!versionId) return
  const res = await api.sendCampaign(client, id, {
    content_version_id: versionId,
    ...(scheduledAt ? { scheduled_at: scheduledAt } : {}),
  })
  dialogOpen.value = false
  await refetch()
  if (res.state === 'scheduled') {
    notify({
      variant: 'success',
      messageId: 'toast.scheduled',
      params: { time: formatTsi(res.scheduled_at ?? scheduledAt ?? '') },
    })
  } else {
    notify({
      variant: 'success',
      messageId: 'toast.sendStarted',
      params: { total: res.deliveries.total },
    })
  }
  void router.push({ name: 'tracking', params: { campaignId: id } })
}

async function onSchedule(scheduledAt: string) {
  const id = campaignId.value
  const c = campaign.value
  if (!id || !c || submitting.value) return
  submitting.value = true
  dialogError.value = null
  try {
    await send(id, c.approval?.content_version_id ?? latest.value?.id, scheduledAt)
  } catch (e) {
    await onRequestError(toApiError(e))
  } finally {
    submitting.value = false
  }
}

/** DELETE …/schedule: no confirmation (a plan is easy to remake); the bar flips back to `approved`. */
async function onCancelSchedule() {
  const id = campaignId.value
  if (!id || submitting.value) return
  submitting.value = true
  try {
    await api.cancelSchedule(client, id)
    await refetch()
    notify({ variant: 'info', messageId: 'toast.scheduleCanceled' })
  } catch (e) {
    const err = toApiError(e)
    reportError(err, 'approval')
    if (err.status === 409 || err.status === 404) {
      await refetch()
      // The worker started the send meanwhile: the plan is gone, tracking is the right place.
      const state = store.campaign?.state
      if (state === 'sending' || state === 'completed') {
        void router.push({ name: 'tracking', params: { campaignId: id } })
      }
    }
  } finally {
    submitting.value = false
  }
}

function goToTracking() {
  const id = campaignId.value
  if (id) void router.push({ name: 'tracking', params: { campaignId: id } })
}

const nextText = computed<string | null>(() =>
  campaign.value && !latest.value && !locked.value ? t('approval.next.noContent') : null,
)

function goToContent() {
  const id = campaignId.value
  if (id) void router.push({ name: 'content', params: { campaignId: id } })
}

onMounted(async () => {
  const id = campaignId.value
  if (!id) return
  const c = await store.load(id)
  if (c?.latest_content_version) void loadPreview()
})
</script>

<template>
  <div data-testid="page-approval" class="mx-auto flex w-full max-w-3xl flex-col gap-6">
    <EmptyState
      v-if="notFound"
      kind="campaign_not_found"
      @action="router.push({ name: 'brief-new' })"
    />
    <template v-else>
      <!-- No status badge here: AppHeader already shows the campaign state. -->
      <StepHeader
        step="approval"
        :role="t('approval.role')"
        :title="t('approval.title')"
        :subtitle="t('approval.subtitle')"
        status-kind="campaign"
        :status="null"
        :next="nextText"
      />

      <section
        data-testid="approval-summary"
        :data-state="campaign?.state ?? 'loading'"
        aria-labelledby="approval-summary-title"
        class="overflow-clip rounded-card-lg border border-border-default bg-surface-raised shadow-raised"
      >
        <div class="h-1 bg-gradient-brand" aria-hidden="true" />
        <div
          class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 px-4 pt-5 pb-4 md:px-8"
        >
          <h2 id="approval-summary-title" class="text-h3 text-accent-heading">
            {{ t('approval.summaryTitle') }}
          </h2>
          <span
            v-if="latest"
            data-testid="approval-version"
            :data-version-id="latest.id"
            :data-value="latest.version_number"
            class="rounded-pill border border-border-default px-2.5 py-0.5 font-mono text-caption text-ink-600"
            >{{ t('approval.version', { n: latest.version_number }) }}</span
          >
        </div>

        <p v-if="campaign && !latest" class="px-4 pb-6 text-body text-ink-600 md:px-8">
          {{ t('approval.noVersion') }}
        </p>

        <!-- Three checks, top to bottom: who receives it, what exactly goes, does it fit. -->
        <ol v-else class="border-t border-border-subtle">
          <!-- 1 · Who -->
          <li
            class="grid scroll-mb-48 gap-3 border-b border-dashed border-border-default px-4 py-5 sm:grid-cols-[9rem_1fr] sm:gap-6 md:px-8"
          >
            <h3 class="flex items-center gap-2 text-overline text-ink-600" lang="tr">
              <Users class="size-4 text-brand-icon" aria-hidden="true" />
              {{ t('approval.row.recipients') }}
            </h3>
            <div class="flex flex-col gap-2">
              <dl v-if="audience" class="flex flex-wrap gap-x-8 gap-y-2">
                <div class="flex items-baseline gap-2">
                  <dt class="flex items-center gap-1.5 text-body-sm text-ink-700">
                    <span class="size-2 rounded-full bg-sms" aria-hidden="true" />
                    {{ t('tabs.sms') }}
                  </dt>
                  <dd class="text-body-sm text-ink-600">
                    <span
                      data-testid="approval-audience-sms"
                      :data-value="audience.sms"
                      class="text-numeral-md text-accent-numeral tabular-nums"
                      >{{ formatNumber(audience.sms) }}</span
                    >
                    {{ t('approval.recipients') }}
                  </dd>
                </div>
                <div class="flex items-baseline gap-2">
                  <dt class="flex items-center gap-1.5 text-body-sm text-ink-700">
                    <span class="size-2 rounded-full bg-email" aria-hidden="true" />
                    {{ t('tabs.email') }}
                  </dt>
                  <dd class="text-body-sm text-ink-600">
                    <span
                      data-testid="approval-audience-email"
                      :data-value="audience.email"
                      class="text-numeral-md text-accent-numeral tabular-nums"
                      >{{ formatNumber(audience.email) }}</span
                    >
                    {{ t('approval.recipients') }}
                  </dd>
                </div>
              </dl>
              <Skeleton v-else class="h-7 w-48" />
              <p class="text-caption text-ink-500">{{ t('approval.recipientsNote') }}</p>
            </div>
          </li>

          <!-- 2 · What: the API-rendered text for one sample recipient, as plain text. -->
          <li
            class="grid scroll-mb-48 gap-3 border-b border-dashed border-border-default px-4 py-5 sm:grid-cols-[9rem_1fr] sm:gap-6 md:px-8"
          >
            <h3 class="flex items-center gap-2 text-overline text-ink-600" lang="tr">
              <FileText class="size-4 text-brand-icon" aria-hidden="true" />
              {{ t('approval.row.message') }}
            </h3>
            <div
              data-testid="approval-preview"
              :data-state="previewState"
              :aria-busy="previewState === 'loading' ? 'true' : undefined"
              class="flex min-w-0 flex-col gap-4"
            >
              <template v-if="preview">
                <p data-testid="approval-preview-sample" class="text-caption text-ink-500">
                  {{ t('approval.sample', { name: sampleName }) }}
                </p>
                <div class="flex flex-col gap-1 border-l-2 border-sms pl-4">
                  <p class="text-caption font-semibold text-sms-text">{{ t('tabs.sms') }}</p>
                  <p
                    data-testid="approval-preview-sms"
                    class="text-body break-words whitespace-pre-line text-ink-900"
                  >
                    {{ preview.sms.text }}
                  </p>
                </div>
                <div class="flex min-w-0 flex-col gap-1 border-l-2 border-email pl-4">
                  <p class="text-caption font-semibold text-email-text">{{ t('tabs.email') }}</p>
                  <p class="text-body break-words text-ink-900">
                    <span class="text-ink-500">{{ t('preview.subject') }}: </span>
                    <span data-testid="approval-preview-email-subject" class="font-semibold">{{
                      preview.email.subject
                    }}</span>
                  </p>
                  <div
                    data-testid="approval-preview-email-body"
                    tabindex="0"
                    role="region"
                    :aria-label="t('approval.emailBody')"
                    class="max-h-72 scroll-mb-48 overflow-y-auto rounded-sm text-body-sm break-words whitespace-pre-line text-ink-700 focus-visible:shadow-focus focus-visible:outline-none"
                  >
                    {{ preview.email.text }}
                  </div>
                </div>
              </template>
              <div v-else-if="previewState === 'loading'" class="flex flex-col gap-3">
                <Skeleton class="h-4 w-32" />
                <Skeleton class="h-16 w-full rounded-sm" />
                <Skeleton class="h-24 w-full rounded-sm" />
              </div>
              <div
                v-else-if="previewError"
                class="flex flex-wrap items-center gap-3 text-body-sm text-error"
              >
                <p role="alert">{{ t(errorPresentation(previewError).messageId) }}</p>
                <Button
                  data-testid="approval-preview-retry"
                  variant="ghost"
                  size="sm"
                  @click="loadPreview"
                >
                  <RotateCw aria-hidden="true" />
                  {{ t('approval.previewRetry') }}
                </Button>
              </div>
              <p v-else class="text-body-sm text-ink-500">{{ t('preview.empty') }}</p>
            </div>
          </li>

          <!-- 3 · Fits: worst case after personalisation, a single pass/fail (REQ-CNT-05). -->
          <li
            v-if="limitCheck"
            class="grid scroll-mb-48 gap-3 px-4 py-5 sm:grid-cols-[9rem_1fr] sm:gap-6 md:px-8"
          >
            <h3 class="flex items-center gap-2 text-overline text-ink-600" lang="tr">
              <component
                :is="limitCheck.pass ? CircleCheck : CircleAlert"
                :class="cn('size-4', limitCheck.pass ? 'text-success' : 'text-error')"
                aria-hidden="true"
              />
              {{ t('approval.row.limits') }}
            </h3>
            <div
              data-testid="approval-limit-check"
              :data-state="limitCheck.pass ? 'pass' : 'fail'"
              :data-sms="limitCheck.sms"
              :data-subject="limitCheck.subject"
              class="flex flex-col gap-1"
            >
              <p
                :class="
                  cn('text-body font-semibold', limitCheck.pass ? 'text-success' : 'text-error')
                "
              >
                {{
                  limitCheck.pass ? t('approval.limitCheck.pass') : t('approval.limitCheck.fail')
                }}
              </p>
              <p class="text-caption text-ink-500">
                <span class="font-mono tabular-nums">{{
                  t('approval.limitCheck.detail', {
                    sms: limitCheck.sms,
                    subject: limitCheck.subject,
                  })
                }}</span>
                · {{ t('approval.limitCheck.hint') }}
              </p>
            </div>
          </li>
        </ol>

        <!-- The decision (DESIGN.md §8.4): last block of the card, sticky to the viewport bottom
             while the card is scrolled past and resting at the card's end. It is opaque and follows
             the last row in the flow, so scrolling always uncovers what it passes over; rows and the
             email body carry scroll-margin so focus never lands under it. At 375 px it is 177 px
             high (22 % of an 812 px viewport, under the 25 % limit), so it stays sticky there too. -->
        <div
          v-if="campaign"
          data-testid="approval-decision"
          class="border-t border-border-default bg-surface-sunken px-4 py-5 sticky bottom-0 z-10 md:px-8"
        >
          <ApprovalBar
            :campaign-state="campaign.state"
            :version-status="latest ? 'validated' : 'none'"
            :dirty="false"
            :submitting="submitting"
            :stale="stale"
            :scheduled-at="campaign.scheduled_at"
            @approve="openDialog('approve')"
            @send="openDialog('send')"
            @cancel-schedule="onCancelSchedule"
            @to-tracking="goToTracking"
          >
            <Button
              v-if="!locked"
              data-testid="approval-edit"
              variant="ghost"
              size="md"
              @click="goToContent"
            >
              <PencilLine aria-hidden="true" />
              {{ t('approval.edit') }}
            </Button>
          </ApprovalBar>
        </div>
      </section>

      <ConfirmDialog
        :open="dialogOpen"
        :kind="dialogKind"
        :counts="dialogKind === 'send' ? sendCounts : null"
        :submitting="submitting"
        :error="dialogError"
        :allow-schedule="dialogKind === 'send'"
        :schedule-min-lead-seconds="scheduleMinLead"
        :schedule-max-ahead-seconds="scheduleMaxAhead"
        @confirm="onConfirm"
        @schedule="onSchedule"
        @cancel="closeDialog"
        @update:open="(v) => !v && closeDialog()"
      />
    </template>
  </div>
</template>
