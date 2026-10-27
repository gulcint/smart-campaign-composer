<script setup lang="ts">
// ui.md §3.20 / DESIGN.md §8.5: delivery counters per channel and in total (REQ-TRK-01).
// data-state: completed (status.state) › stale (connection not open) › live. Raw counts live in
// data-value; the screen shows them tr-TR formatted. The polite summary is throttled to one
// announcement per 10 s (DESIGN.md §9), the completion is announced politely, never assertively.
import { CircleCheck, Flag, Radio, Unplug } from '@lucide/vue'
import { computed, inject, onBeforeUnmount, ref, watch } from 'vue'
import { routerKey } from 'vue-router'

import type { CampaignStatus, ChannelKey, ConnectionState, StatusCounts } from '@/api/types'
import { formatNumber, t } from '@/i18n'
import { cn } from '@/lib/utils'

import ProgressBar from './ProgressBar.vue'

const props = defineProps<{
  status: CampaignStatus | null
  connection: ConnectionState
  lastUpdatedAt: string | null
}>()

type CountKey = keyof StatusCounts

const state = computed<'completed' | 'stale' | 'live'>(() => {
  if (props.status?.state === 'completed') return 'completed'
  return props.connection === 'open' ? 'live' : 'stale'
})

const ZERO: StatusCounts = { total: 0, queued: 0, sending: 0, sent: 0, failed: 0 }
const totals = computed<StatusCounts>(() => props.status?.counts ?? ZERO)
const finished = (c: StatusCounts) => c.sent + c.failed
const percent = (c: StatusCounts) =>
  c.total > 0 ? Math.floor((finished(c) / c.total) * 100) : state.value === 'completed' ? 100 : 0

const CHANNELS: { key: ChannelKey; label: string; dot: string }[] = [
  { key: 'sms', label: t('tabs.sms'), dot: 'bg-sms' },
  { key: 'email', label: t('tabs.email'), dot: 'bg-email' },
]
const channels = computed(() =>
  CHANNELS.map((c) => ({ ...c, counts: props.status?.by_channel[c.key] ?? ZERO })),
)

/** Status order on a card: outcome first (sent, failed), then the work left. */
const ROWS: { key: Exclude<CountKey, 'total'>; dot: string }[] = [
  { key: 'sent', dot: 'bg-success' },
  { key: 'failed', dot: 'bg-error' },
  { key: 'sending', dot: 'bg-info' },
  { key: 'queued', dot: 'bg-ink-300' },
]

/** Failed counts are error-coloured only when non-zero (DESIGN.md §8.5); others stay numerals. */
const valueClass = (key: CountKey, n: number) =>
  key === 'failed' && n > 0 ? 'text-error' : 'text-accent-numeral'

const show = (n: number) => (props.status ? formatNumber(n) : '—')

const updatedText = computed(() => {
  if (!props.lastUpdatedAt) return null
  const d = new Date(props.lastUpdatedAt)
  if (Number.isNaN(d.getTime())) return null
  return t('delivery.lastUpdated', {
    time: d.toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
  })
})

// "?status=failed" on the tracking route opens the failed-delivery list (ui.md §3.20). Without
// an installed router (component tests) the link is a plain anchor.
const router = inject(routerKey, null)
const failedHref = computed(() =>
  props.status
    ? `/campaigns/${encodeURIComponent(props.status.campaign_id)}/tracking?status=failed`
    : '#',
)
function openFailed(ev: MouseEvent) {
  if (!router || !props.status || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.button !== 0) return
  ev.preventDefault()
  void router.push(failedHref.value)
}

// ---- screen reader summary (DESIGN.md §9) ----
const ANNOUNCE_MS = 10_000
const announcement = ref('')
let lastAnnounced = 0
let pending: ReturnType<typeof setTimeout> | null = null

function summary(): string {
  const s = props.status
  if (!s) return ''
  const parts = channels.value
    .filter((c) => c.counts.total > 0)
    .map((c) =>
      t('delivery.announce', {
        channel: c.label,
        sent: formatNumber(c.counts.sent),
        failed: formatNumber(c.counts.failed),
      }),
    )
  if (s.state === 'completed') parts.unshift(t('delivery.completedTitle'))
  return parts.join('. ')
}

function announce() {
  pending = null
  lastAnnounced = Date.now()
  announcement.value = summary()
}

watch(
  () => props.status,
  (s) => {
    if (!s) return
    if (s.state === 'completed') {
      if (pending) clearTimeout(pending)
      announce()
      return
    }
    if (pending) return
    const wait = Math.max(0, lastAnnounced + ANNOUNCE_MS - Date.now())
    pending = setTimeout(announce, wait)
  },
)

onBeforeUnmount(() => {
  if (pending) clearTimeout(pending)
})
</script>

<template>
  <section
    data-testid="delivery-counters"
    :data-state="state"
    :aria-busy="status ? undefined : 'true'"
    aria-labelledby="delivery-counters-title"
    class="flex flex-col gap-4"
  >
    <span class="sr-only" role="status" aria-live="polite">{{ announcement }}</span>

    <!-- Overall: title, live/stale indicator, total progress and the five totals. -->
    <div
      :class="
        cn(
          'overflow-hidden rounded-card-lg border border-border-default bg-surface-raised shadow-raised transition-opacity',
          state === 'stale' && 'opacity-80',
        )
      "
    >
      <div class="h-1 bg-gradient-brand" aria-hidden="true" />
      <div class="flex flex-col gap-4 p-4 md:p-6">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h2
            v-if="state === 'completed'"
            id="delivery-counters-title"
            data-testid="delivery-completed-title"
            class="flex items-center gap-2 text-h3 text-accent-heading"
          >
            <Flag class="size-5 text-brand-icon" aria-hidden="true" />
            {{ t('delivery.completedTitle') }}
          </h2>
          <h2
            v-else
            id="delivery-counters-title"
            class="flex items-center gap-2 text-h3 text-accent-heading"
          >
            {{ status ? t('delivery.liveTitle') : t('delivery.waiting') }}
          </h2>

          <p
            v-if="state === 'live'"
            class="inline-flex items-center gap-1.5 rounded-pill bg-success-bg px-2.5 py-1 text-caption font-medium text-success"
          >
            <Radio class="size-3.5" aria-hidden="true" />
            {{ t('delivery.live') }}
          </p>
          <p
            v-else-if="state === 'completed'"
            class="inline-flex items-center gap-1.5 text-body-sm text-ink-600"
          >
            <CircleCheck class="size-4 text-success" aria-hidden="true" />
            {{
              t('delivery.completedSummary', {
                sent: formatNumber(totals.sent),
                failed: formatNumber(totals.failed),
              })
            }}
          </p>
        </div>

        <p
          v-if="state === 'stale'"
          data-testid="delivery-stale-note"
          class="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-card bg-warning-bg px-3 py-2 text-body-sm text-warning"
        >
          <Unplug class="size-4 shrink-0" aria-hidden="true" />
          <span class="font-medium">{{ t('delivery.staleNote') }}</span>
          <span v-if="updatedText" class="text-ink-600">· {{ updatedText }}</span>
        </p>

        <div class="flex flex-col gap-2">
          <div class="flex items-baseline justify-between gap-3">
            <span class="text-body-sm font-medium text-ink-700">{{
              t('delivery.totalLabel')
            }}</span>
            <span class="text-numeral-sm text-ink-600 tabular-nums">{{
              t('delivery.done', { p: percent(totals) })
            }}</span>
          </div>
          <ProgressBar
            data-testid="delivery-bar-total"
            :value="finished(totals)"
            :max="totals.total"
            variant="segmented"
            :segments="totals"
            :label="
              t('delivery.barLabel', {
                channel: t('delivery.totalLabel'),
                done: formatNumber(finished(totals)),
                total: formatNumber(totals.total),
              })
            "
          />
        </div>

        <dl class="grid grid-cols-2 gap-3 sm:grid-cols-5">
          <div class="flex flex-col gap-0.5">
            <dt class="text-caption text-ink-500">{{ t('delivery.count.total') }}</dt>
            <dd
              data-testid="delivery-count-total-total"
              :data-value="totals.total"
              class="text-numeral-md text-accent-numeral tabular-nums"
            >
              {{ show(totals.total) }}
            </dd>
          </div>
          <div v-for="r in ROWS" :key="r.key" class="flex flex-col gap-0.5">
            <dt class="flex items-center gap-1.5 text-caption text-ink-500">
              <span :class="cn('size-2 rounded-full', r.dot)" aria-hidden="true" />
              {{ t(`status.delivery.${r.key}`) }}
            </dt>
            <dd
              :data-testid="`delivery-count-total-${r.key}`"
              :data-value="totals[r.key]"
              :class="cn('text-numeral-md tabular-nums', valueClass(r.key, totals[r.key]))"
            >
              {{ show(totals[r.key]) }}
            </dd>
          </div>
        </dl>

        <a
          v-if="state === 'completed' && totals.failed > 0"
          data-testid="delivery-failed-link"
          :href="failedHref"
          class="inline-flex w-fit items-center gap-1.5 text-body-sm font-medium text-error underline underline-offset-4 hover:text-error-strong"
          @click="openFailed"
          >{{ t('delivery.failedLink') }} ({{ formatNumber(totals.failed) }})</a
        >
      </div>
    </div>

    <!-- Per channel (DESIGN.md §8.5): side by side ≥768 px, stacked below. -->
    <div class="grid gap-4 md:grid-cols-2">
      <article
        v-for="c in channels"
        :key="c.key"
        :data-testid="`delivery-card-${c.key}`"
        :aria-labelledby="`delivery-card-${c.key}-title`"
        :class="
          cn(
            'flex min-w-0 flex-col gap-4 rounded-card border border-border-default bg-surface-card p-4 transition-opacity md:p-6',
            state === 'stale' && 'opacity-80',
          )
        "
      >
        <div class="flex items-baseline justify-between gap-3">
          <h3
            :id="`delivery-card-${c.key}-title`"
            class="flex items-center gap-2 text-h4 text-ink-900"
          >
            <span :class="cn('size-2.5 rounded-full', c.dot)" aria-hidden="true" />
            {{ c.label }}
          </h3>
          <p class="text-body-sm text-ink-600">
            <span
              :data-testid="`delivery-count-${c.key}-total`"
              :data-value="c.counts.total"
              class="text-numeral-md text-accent-numeral tabular-nums"
              >{{ show(c.counts.total) }}</span
            >
            {{ t('delivery.recipients') }}
          </p>
        </div>

        <div class="flex items-center gap-3">
          <ProgressBar
            :data-testid="`delivery-bar-${c.key}`"
            :value="finished(c.counts)"
            :max="c.counts.total"
            variant="segmented"
            :segments="c.counts"
            :label="
              t('delivery.barLabel', {
                channel: c.label,
                done: formatNumber(finished(c.counts)),
                total: formatNumber(c.counts.total),
              })
            "
          />
          <span class="w-12 shrink-0 text-right text-numeral-sm text-ink-600 tabular-nums"
            >%{{ percent(c.counts) }}</span
          >
        </div>

        <dl class="grid grid-cols-2 gap-x-4 gap-y-2">
          <div
            v-for="r in ROWS"
            :key="r.key"
            class="flex items-baseline justify-between gap-2 border-b border-border-subtle pb-1.5"
          >
            <dt class="flex items-center gap-1.5 text-body-sm text-ink-600">
              <span :class="cn('size-2 rounded-full', r.dot)" aria-hidden="true" />
              {{ t(`status.delivery.${r.key}`) }}
            </dt>
            <dd
              :data-testid="`delivery-count-${c.key}-${r.key}`"
              :data-value="c.counts[r.key]"
              :class="cn('text-numeral-sm tabular-nums', valueClass(r.key, c.counts[r.key]))"
            >
              {{ show(c.counts[r.key]) }}
            </dd>
          </div>
        </dl>
      </article>
    </div>
  </section>
</template>
