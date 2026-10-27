<script setup lang="ts">
// Step 4 (ui.md §3.24, §3.17, §3.18; DECISIONS D45; DESIGN.md §8.4): always approves and
// sends the campaign's latest content version; `stale` appears only after a 409.
import { Check, FileCheck2, PencilLine } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { toApiError } from '@/api/errors'
import type { Preview } from '@/api/types'
import ApprovalBar from '@/components/ApprovalBar.vue'
import BrandMark from '@/components/BrandMark.vue'
import Button from '@/components/Button.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmailPreview from '@/components/EmailPreview.vue'
import EmptyState from '@/components/EmptyState.vue'
import LimitChip from '@/components/LimitChip.vue'
import SmsPreview from '@/components/SmsPreview.vue'
import StepHeader from '@/components/StepHeader.vue'
import { useNotify } from '@/composables/useNotify'
import { formatNumber, t } from '@/i18n'
import { walkedSamples } from '@/lib/preview'
import { isLockedForEditing } from '@/lib/steps'
import { runeCount } from '@/lib/text-cap'
import { cn } from '@/lib/utils'
import { campaignIdOf } from '@/router'
import { injectApiClient, reportError } from '@/stores/api'
import { useCampaignStore } from '@/stores/campaign'

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()
const client = injectApiClient()
const { notify } = useNotify()

const campaignId = computed(() => (route ? campaignIdOf(route) : null))
const campaign = computed(() =>
  campaignId.value && store.campaign?.id === campaignId.value ? store.campaign : null,
)
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))
const latest = computed(() => campaign.value?.latest_content_version ?? null)
const audience = computed(() => campaign.value?.segment?.audience ?? null)
const approved = computed(() => campaign.value?.state === 'approved')
const locked = computed(() => isLockedForEditing(campaign.value?.state ?? null))

// Read-only length status of the version to be sent (DESIGN.md §8.4): template runes (same
// NFC count as CharCounter) and the API's worst case after personalisation (REQ-CNT-05).
const limits = computed(() => {
  const v = latest.value
  if (!v) return null
  return {
    sms: runeCount(v.content.sms.body),
    subject: runeCount(v.content.email.subject),
    smsWorst: v.worst_case?.sms_runes ?? null,
    subjectWorst: v.worst_case?.subject_runes ?? null,
  }
})

// ---- preview (ui.md §1.1) ----
const preview = ref<Preview | null>(null)
const previewLoading = ref(false)
const previewError = ref<ApiError | null>(null)
let previewSeq = 0

async function loadPreview(index: number) {
  const id = campaignId.value
  if (!id) return
  const seq = ++previewSeq
  previewLoading.value = true
  try {
    const p = await api.previewContent(client, id, index)
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

function onNextSample() {
  if (previewError.value?.code === 'customer_not_found') {
    void loadPreview(0)
    return
  }
  const p = preview.value
  if (!p || p.sample_count <= 1) return
  void loadPreview((p.sample_index + 1) % walkedSamples(p.sample_count))
}

// ---- approve / send (ui.md §3.24) ----
const dialogKind = ref<'approve' | 'send'>('approve')
const dialogOpen = ref(false)
const dialogError = ref<ApiError | null>(null)
const submitting = ref(false)
const stale = ref(false)

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
      const versionId = c.approval?.content_version_id ?? latest.value?.id
      if (!versionId) return
      await api.sendCampaign(client, id, { content_version_id: versionId })
      dialogOpen.value = false
      await refetch()
      void router.push({ name: 'tracking', params: { campaignId: id } })
    }
  } catch (e) {
    await onRequestError(toApiError(e))
  } finally {
    submitting.value = false
  }
}

// The approve → send list below is the next-action indicator; the pill only shows when
// there is nothing to decide on yet.
const nextText = computed<string | null>(() =>
  campaign.value && !latest.value && !locked.value ? t('approval.next.noContent') : null,
)

/** Approve → send as two visible decisions; the approve step is done once approved. */
const decisionSteps = computed(() => {
  const done = approved.value && !stale.value
  return [
    { id: 'approve', label: t('approval.approve'), done, current: !done },
    { id: 'send', label: t('approval.send'), done: false, current: done },
  ]
})

function goToContent() {
  const id = campaignId.value
  if (id) void router.push({ name: 'content', params: { campaignId: id } })
}

onMounted(async () => {
  const id = campaignId.value
  if (!id) return
  const c = await store.load(id)
  if (c?.latest_content_version) void loadPreview(0)
})
</script>

<template>
  <div data-testid="page-approval" class="mx-auto flex w-full max-w-5xl flex-col gap-6">
    <EmptyState
      v-if="notFound"
      kind="campaign_not_found"
      @action="router.push({ name: 'brief-new' })"
    />
    <template v-else>
      <StepHeader
        step="approval"
        :role="t('approval.role')"
        :title="t('approval.title')"
        :subtitle="t('approval.subtitle')"
        status-kind="campaign"
        :status="campaign?.state ?? null"
        :next="nextText"
      >
        <ol
          v-if="latest && !locked"
          :aria-label="t('approval.decision')"
          class="flex items-center gap-2 text-body-sm"
        >
          <li v-for="(d, i) in decisionSteps" :key="d.id" class="flex items-center gap-2">
            <span
              :aria-current="d.current ? 'step' : undefined"
              :class="
                cn(
                  'inline-flex items-center gap-1.5 rounded-pill px-2.5 py-1',
                  d.done && 'bg-success-bg text-success',
                  d.current && 'bg-secondary font-semibold text-accent-text',
                  !d.done && !d.current && 'text-ink-500',
                )
              "
            >
              <Check v-if="d.done" class="size-3.5" aria-hidden="true" />
              <span v-else class="font-mono text-caption" aria-hidden="true">{{ i + 1 }}</span>
              {{ d.label }}
            </span>
            <span v-if="i === 0" class="h-px w-4 bg-border-default" aria-hidden="true" />
          </li>
        </ol>
      </StepHeader>

      <section
        data-testid="approval-summary"
        :data-state="campaign?.state ?? 'loading'"
        aria-labelledby="approval-summary-title"
        class="overflow-hidden rounded-card-lg border border-border-default bg-surface-raised shadow-raised"
      >
        <div class="h-1 bg-gradient-brand" aria-hidden="true" />
        <div
          class="flex flex-wrap items-center gap-3 border-b border-border-subtle px-4 py-4 md:px-6"
        >
          <BrandMark :size="28" />
          <h2 id="approval-summary-title" class="text-h3 text-accent-heading">
            {{ t('approval.summaryTitle') }}
          </h2>
          <p
            v-if="latest"
            class="flex flex-wrap items-center gap-2 text-body-sm text-ink-600 sm:ml-auto"
          >
            <FileCheck2 class="size-4 text-ink-500" aria-hidden="true" />
            <span
              data-testid="approval-version"
              :data-version-id="latest.id"
              :data-value="latest.version_number"
              class="font-semibold text-ink-900"
              >{{ t('approval.version', { n: latest.version_number }) }}</span
            >
            <span aria-hidden="true">·</span>
            <span>{{ t(`approval.source.${latest.source}`) }}</span>
          </p>
        </div>

        <p v-if="campaign && !latest" class="px-4 py-6 text-body text-ink-600 md:px-6">
          {{ t('approval.noVersion') }}
        </p>

        <div v-else class="grid md:grid-cols-2">
          <!-- SMS: recipients, length status, the exact text once. -->
          <section
            aria-labelledby="approval-sms-title"
            class="flex min-w-0 flex-col gap-4 border-border-subtle p-4 md:border-r md:p-6"
          >
            <div class="flex items-baseline justify-between gap-3">
              <h3 id="approval-sms-title" class="flex items-center gap-2 text-h4 text-ink-900">
                <span class="size-2.5 rounded-full bg-sms" aria-hidden="true" />
                {{ t('tabs.sms') }}
              </h3>
              <p v-if="audience" class="text-body-sm text-ink-600">
                <span
                  data-testid="approval-audience-sms"
                  :data-value="audience.sms"
                  class="text-numeral-md text-accent-numeral tabular-nums"
                  >{{ formatNumber(audience.sms) }}</span
                >
                {{ t('approval.recipients') }}
              </p>
            </div>
            <div v-if="limits" class="flex flex-wrap gap-2">
              <LimitChip
                data-testid="approval-limit-sms"
                :label="t('approval.limit.template')"
                :value="limits.sms"
                :limit="160"
              />
              <LimitChip
                v-if="limits.smsWorst !== null"
                data-testid="approval-limit-sms-worst"
                :label="t('approval.limit.worst')"
                :value="limits.smsWorst"
                :limit="160"
              />
            </div>
            <div class="flex flex-col gap-2">
              <p class="text-overline text-ink-500" lang="tr">{{ t('approval.finalText') }}</p>
              <SmsPreview
                :preview="preview"
                :loading="previewLoading"
                :error="previewError"
                @next-sample="onNextSample"
              />
            </div>
          </section>

          <!-- Email: recipients, subject length status, the rendered email once. -->
          <section
            aria-labelledby="approval-email-title"
            class="flex min-w-0 flex-col gap-4 border-t border-border-subtle p-4 md:border-t-0 md:p-6"
          >
            <div class="flex items-baseline justify-between gap-3">
              <h3 id="approval-email-title" class="flex items-center gap-2 text-h4 text-ink-900">
                <span class="size-2.5 rounded-full bg-email" aria-hidden="true" />
                {{ t('tabs.email') }}
              </h3>
              <p v-if="audience" class="text-body-sm text-ink-600">
                <span
                  data-testid="approval-audience-email"
                  :data-value="audience.email"
                  class="text-numeral-md text-accent-numeral tabular-nums"
                  >{{ formatNumber(audience.email) }}</span
                >
                {{ t('approval.recipients') }}
              </p>
            </div>
            <div v-if="limits" class="flex flex-wrap gap-2">
              <LimitChip
                data-testid="approval-limit-email-subject"
                :label="t('approval.limit.subject')"
                :value="limits.subject"
                :limit="80"
              />
              <LimitChip
                v-if="limits.subjectWorst !== null"
                data-testid="approval-limit-email-subject-worst"
                :label="t('approval.limit.subjectWorst')"
                :value="limits.subjectWorst"
                :limit="80"
              />
            </div>
            <div class="flex flex-col gap-2">
              <p class="text-overline text-ink-500" lang="tr">{{ t('approval.finalText') }}</p>
              <EmailPreview :preview="preview" :loading="previewLoading" :error="previewError" />
            </div>
          </section>
        </div>

        <div
          class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-border-subtle bg-surface-sunken/60 px-4 py-3 md:px-6"
        >
          <p class="text-body-sm text-ink-600">
            {{ approved && !stale ? t('approval.editHintApproved') : t('approval.editHint') }}
          </p>
          <Button data-testid="approval-edit" variant="ghost" size="sm" @click="goToContent">
            <PencilLine aria-hidden="true" />
            {{ t('approval.edit') }}
          </Button>
        </div>
      </section>

      <ApprovalBar
        v-if="campaign"
        :campaign-state="campaign.state"
        :version-status="latest ? 'validated' : 'none'"
        :dirty="false"
        :submitting="submitting"
        :stale="stale"
        @approve="openDialog('approve')"
        @send="openDialog('send')"
      />

      <ConfirmDialog
        :open="dialogOpen"
        :kind="dialogKind"
        :counts="dialogKind === 'send' ? sendCounts : null"
        :submitting="submitting"
        :error="dialogError"
        @confirm="onConfirm"
        @cancel="closeDialog"
        @update:open="(v) => !v && closeDialog()"
      />
    </template>
  </div>
</template>
