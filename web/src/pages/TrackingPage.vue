<script setup lang="ts">
// Step 5 (ui.md §1, §3.20, §5.3; DESIGN.md §8.5): live delivery counters over the status SSE
// stream. A dropped stream turns the counters stale and shows the ConnectionBanner; the
// composable reconnects with backoff. `?status=failed` lists the failed deliveries (§3.20 link).
// The ActivityFeed is out of scope by owner decision (counters only).
import { X } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { toApiError } from '@/api/errors'
import type { Delivery } from '@/api/types'
import Button from '@/components/Button.vue'
import DeliveryCounters from '@/components/DeliveryCounters.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorCard from '@/components/ErrorCard.vue'
import Skeleton from '@/components/Skeleton.vue'
import StepHeader from '@/components/StepHeader.vue'
import { useCampaignStatusStream } from '@/composables/useCampaignStatusStream'
import type { MessageId } from '@/i18n'
import { formatNumber, hasMessage, t } from '@/i18n'
import { campaignIdOf } from '@/router'
import { injectApiClient } from '@/stores/api'
import { useCampaignStore } from '@/stores/campaign'
import { useUiStore } from '@/stores/ui'

const FAILED_LIMIT = 50

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()
const ui = useUiStore()
const client = injectApiClient()

const campaignId = computed(() => (route ? (campaignIdOf(route) ?? '') : ''))
const campaign = computed(() =>
  campaignId.value && store.campaign?.id === campaignId.value ? store.campaign : null,
)
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))

const { status, connection, lastUpdatedAt, completed } = useCampaignStatusStream(campaignId)

// The stream's state is fresher than the loaded campaign (it moves to completed live).
const campaignState = computed(() => status.value?.state ?? campaign.value?.state ?? null)

// ConnectionBanner (ui.md §4 network_error): only once a live connection was lost, not while
// the first connection is still being made.
const wasOpen = ref(false)
watch(connection, (c) => {
  if (c === 'open') wasOpen.value = true
  ui.connectionLost = c === 'error' || (c === 'connecting' && wasOpen.value)
})

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

async function loadFailed() {
  const id = campaignId.value
  if (!id) return
  failedLoading.value = true
  failedError.value = null
  try {
    const page = await api.listDeliveries(client, id, {
      status: 'failed',
      order: 'recent',
      limit: FAILED_LIMIT,
    })
    failed.value = page.items
    failedTotal.value = page.total
  } catch (e) {
    failedError.value = toApiError(e)
  } finally {
    failedLoading.value = false
  }
}

watch(
  [showFailed, completed],
  ([show]) => {
    if (show) void loadFailed()
  },
  { immediate: true },
)

function closeFailed() {
  void router.replace({ query: { ...route.query, status: undefined } })
}

function reasonText(reason: string | null): string {
  if (!reason) return '—'
  const id = `delivery.failureReason.${reason}`
  return hasMessage(id) ? t(id as MessageId) : reason
}

onMounted(() => {
  if (campaignId.value) void store.load(campaignId.value)
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

      <DeliveryCounters
        :status="status"
        :connection="connection"
        :last-updated-at="lastUpdatedAt"
      />

      <section
        v-if="showFailed"
        data-testid="delivery-failed-list"
        aria-labelledby="delivery-failed-title"
        class="flex flex-col gap-3 rounded-card border border-border-default bg-surface-card p-4 md:p-6"
      >
        <div class="flex items-center justify-between gap-3">
          <h2 id="delivery-failed-title" class="text-h4 text-accent-heading">
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
          <div class="overflow-x-auto">
            <table class="w-full min-w-[28rem] text-left text-body-sm">
              <thead class="text-caption text-ink-500">
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
                  class="border-b border-border-subtle last:border-b-0"
                >
                  <td class="py-2 pr-3 font-mono text-caption">{{ d.customer_id }}</td>
                  <td class="py-2 pr-3">
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
                  <td class="py-2 pr-3 text-right tabular-nums">
                    {{ formatNumber(d.attempts) }}
                  </td>
                  <td class="py-2">{{ reasonText(d.failure_reason) }}</td>
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
