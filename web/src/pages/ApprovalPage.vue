<script setup lang="ts">
// Step 4 (ui.md §3.24, §3.17, §3.18; DECISIONS D45; DESIGN.md §8.4): always approves and
// sends the campaign's latest content version; `stale` appears only after a 409.
import { PencilLine } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { toApiError } from '@/api/errors'
import type { Preview } from '@/api/types'
import ApprovalBar from '@/components/ApprovalBar.vue'
import Button from '@/components/Button.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmailPreview from '@/components/EmailPreview.vue'
import EmptyState from '@/components/EmptyState.vue'
import SmsPreview from '@/components/SmsPreview.vue'
import { useNotify } from '@/composables/useNotify'
import { formatNumber, t } from '@/i18n'
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
  void loadPreview((p.sample_index + 1) % p.sample_count)
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
      <header class="flex flex-col gap-1 pt-4">
        <h1 data-testid="page-title" tabindex="-1" class="text-h1 outline-none">
          {{ t('approval.title') }}
        </h1>
        <p class="text-body text-ink-600">{{ t('approval.subtitle') }}</p>
      </header>

      <section
        class="flex flex-col gap-5 rounded-card border border-border-default bg-surface-card p-4 shadow-card md:p-6"
        :aria-label="t('approval.title')"
      >
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p
            v-if="latest"
            data-testid="approval-version"
            :data-version-id="latest.id"
            :data-value="latest.version_number"
            class="text-h4 text-accent-heading"
          >
            {{ t('approval.version', { n: latest.version_number }) }}
          </p>
          <Button data-testid="approval-edit" variant="secondary" size="sm" @click="goToContent">
            <PencilLine aria-hidden="true" />
            {{ t('approval.edit') }}
          </Button>
        </div>
        <p v-if="audience" class="text-body text-ink-700">
          {{ t('approval.audience') }} · {{ t('audience.channelSms') }}
          <span
            data-testid="approval-audience-sms"
            :data-value="audience.sms"
            class="font-semibold text-accent-numeral tabular-nums"
            >{{ formatNumber(audience.sms) }}</span
          >
          · {{ t('audience.channelEmail') }}
          <span
            data-testid="approval-audience-email"
            :data-value="audience.email"
            class="font-semibold text-accent-numeral tabular-nums"
            >{{ formatNumber(audience.email) }}</span
          >
        </p>
        <div class="grid gap-6 lg:grid-cols-2">
          <div class="flex flex-col gap-2">
            <h2 class="text-h4 text-accent-heading">{{ t('tabs.sms') }}</h2>
            <SmsPreview
              :preview="preview"
              :loading="previewLoading"
              :error="previewError"
              @next-sample="onNextSample"
            />
          </div>
          <div class="flex flex-col gap-2">
            <h2 class="text-h4 text-accent-heading">{{ t('tabs.email') }}</h2>
            <EmailPreview :preview="preview" :loading="previewLoading" :error="previewError" />
          </div>
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
