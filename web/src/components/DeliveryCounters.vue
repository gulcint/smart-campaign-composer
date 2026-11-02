<script setup lang="ts">
// ui.md §3.20 / DESIGN.md §8.5: delivery counters per channel and in total (REQ-TRK-01).
// data-state: completed (status.state) › scheduled (P7a: the plan lives in SchedulePanel, no
// counter cards) › stale (connection not open) › live. Raw counts live in
// data-value; the screen shows them tr-TR formatted. The polite summary is throttled to one
// announcement per 10 s (DESIGN.md §9), the completion is announced politely, never assertively.
// P10 (REQ-DSH-03): one accented overall card (fixed "Genel durum" h2 with the liveness beside
// it, the completion percentage as the big number, results set apart from the work left), then
// one card per channel. Staleness never dims a card (contrast stays); icon and text say it.
import { ArrowRight, CircleCheck, Clock, Unplug } from '@lucide/vue'
import { computed, inject, onBeforeUnmount, ref, watch } from 'vue'
import { routerKey } from 'vue-router'

import type {
  CampaignState,
  CampaignStatus,
  ChannelKey,
  ConnectionState,
  StatusCounts,
} from '@/api/types'
import { formatNumber, messageParts, t } from '@/i18n'
import { cn } from '@/lib/utils'

import ProgressBar from './ProgressBar.vue'

const props = defineProps<{
  status: CampaignStatus | null
  connection: ConnectionState
  lastUpdatedAt: string | null
  /** The page's view state when the stream has not answered yet (campaign.state); optional. */
  viewState?: CampaignState | null
}>()

type CountKey = keyof StatusCounts

const state = computed<'completed' | 'scheduled' | 'stale' | 'live'>(() => {
  if (props.status?.state === 'completed') return 'completed'
  if ((props.viewState ?? props.status?.state) === 'scheduled') return 'scheduled'
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
  { key: 'queued', dot: 'bg-neutral-status' },
]
const RESULTS = ROWS.slice(0, 2)
const REMAINING = ROWS.slice(2)

/**
 * Number colour rule (ui.md §3.20, P10): sent in the accent numeral colour, failed in --error only
 * when non-zero (ink otherwise), the work left in ink-700. Every number carries its label.
 */
const valueClass = (key: CountKey, n: number) => {
  if (key === 'sent') return 'text-accent-numeral'
  if (key === 'failed') return n > 0 ? 'text-error' : 'text-ink-900'
  if (key === 'total') return 'text-ink-900'
  return 'text-ink-700'
}

// Sentences whose numbers are their own elements (testids, numeral style); the joined text is
// still exactly t(id, params).
const PROCESSED = messageParts('tracking.processed')
const CHANNEL_LINE = messageParts('tracking.channelProcessed')

const show = (n: number) => (props.status ? formatNumber(n) : '—')
/** Raw value for data-value; omitted until the first status arrives (no fake zeros). */
const dv = (n: number) => (props.status ? n : undefined)

// The stale note means "these numbers may be old": not shown while the very first connection is
// still being made (no numbers yet), but shown when that first attempt failed (ui.md §3.20).
const staleNote = computed(
  () => state.value === 'stale' && (props.status !== null || props.connection === 'error'),
)

const updatedText = computed(() => {
  if (!props.lastUpdatedAt) return null
  const d = new Date(props.lastUpdatedAt)
  if (Number.isNaN(d.getTime())) return t('delivery.lastUpdated', { time: props.lastUpdatedAt })
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
    :aria-busy="status || state === 'scheduled' ? undefined : 'true'"
    :aria-labelledby="state === 'scheduled' ? undefined : 'delivery-counters-title'"
    :class="state === 'scheduled' ? 'contents' : 'flex flex-col gap-4'"
  >
    <template v-if="state !== 'scheduled'">
      <span class="sr-only" role="status" aria-live="polite">{{ announcement }}</span>

      <!-- Overall: the one accented card (radius-card-lg + gradient strip, DESIGN.md §8.5). -->
      <div
        class="overflow-hidden rounded-card-lg border border-border-default bg-surface-raised shadow-card"
      >
        <div class="h-1 bg-gradient-brand" aria-hidden="true" />
        <div class="flex flex-col gap-5 p-4 md:p-6">
          <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
            <h2 id="delivery-counters-title" class="text-h2 text-accent-heading">
              {{ t('tracking.overallTitle') }}
            </h2>

            <p
              v-if="state === 'live'"
              data-testid="delivery-live"
              class="inline-flex items-center gap-1.5 rounded-pill bg-success-bg px-2.5 py-1 text-caption font-medium text-success"
            >
              <span class="relative flex size-2" aria-hidden="true">
                <span
                  class="absolute inline-flex size-full rounded-full bg-success opacity-40 motion-safe:animate-ping"
                />
                <span class="relative inline-flex size-2 rounded-full bg-success" />
              </span>
              {{ t('delivery.live') }}
            </p>
            <p
              v-else-if="state === 'completed'"
              data-testid="delivery-completed-title"
              class="inline-flex items-center gap-1.5 rounded-pill bg-brand-status-bg px-2.5 py-1 text-caption font-medium text-brand-status"
            >
              <CircleCheck class="size-3.5 shrink-0" aria-hidden="true" />
              {{ t('delivery.completedTitle') }}
            </p>
            <p
              v-else-if="staleNote"
              data-testid="delivery-stale-note"
              class="inline-flex items-center gap-1.5 rounded-pill bg-warning-bg px-2.5 py-1 text-caption font-medium text-warning"
            >
              <Unplug class="size-3.5 shrink-0" aria-hidden="true" />
              {{ t('delivery.staleNote') }}
            </p>
          </div>

          <p v-if="!status" data-testid="delivery-waiting" role="status" class="sr-only">
            {{ t('delivery.waiting') }}
          </p>

          <div class="flex flex-col gap-3">
            <div class="flex flex-wrap items-baseline gap-x-4 gap-y-1">
              <p
                data-testid="delivery-percent"
                :data-value="dv(percent(totals))"
                class="text-numeral-xl text-accent-numeral tabular-nums max-md:text-[32px] max-md:leading-9"
              >
                {{ status ? `%${percent(totals)}` : '—' }}
              </p>
              <p
                data-testid="delivery-processed"
                :data-value="dv(finished(totals))"
                class="text-body text-ink-700"
              >
                <template v-for="(part, i) in PROCESSED" :key="i"
                  ><span
                    v-if="part.key === 'done'"
                    class="font-semibold text-ink-900 tabular-nums"
                    >{{ show(finished(totals)) }}</span
                  ><span
                    v-else-if="part.key === 'total'"
                    data-testid="delivery-count-total-total"
                    :data-value="dv(totals.total)"
                    class="font-semibold text-ink-900 tabular-nums"
                    >{{ show(totals.total) }}</span
                  ><template v-else>{{ part.text }}</template></template
                >
              </p>
            </div>
            <ProgressBar
              data-testid="delivery-bar-total"
              :value="finished(totals)"
              :max="totals.total"
              variant="segmented"
              size="lg"
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

          <!-- Results (numeral-md) are set apart from the work left (numeral-sm, ink-700). -->
          <div class="grid gap-4 md:grid-cols-2 md:gap-0">
            <div class="flex flex-col gap-2 md:pr-6">
              <p id="delivery-results-title" class="text-overline text-ink-500" lang="tr">
                {{ t('tracking.results') }}
              </p>
              <dl
                data-testid="delivery-results"
                aria-labelledby="delivery-results-title"
                class="grid grid-cols-2 gap-4"
              >
                <div v-for="r in RESULTS" :key="r.key" class="flex flex-col gap-0.5">
                  <dt class="flex items-center gap-1.5 text-body-sm text-ink-600">
                    <span :class="cn('size-2 shrink-0 rounded-full', r.dot)" aria-hidden="true" />
                    {{ t(`status.delivery.${r.key}`) }}
                  </dt>
                  <dd
                    :data-testid="`delivery-count-total-${r.key}`"
                    :data-value="dv(totals[r.key])"
                    :class="cn('text-numeral-md tabular-nums', valueClass(r.key, totals[r.key]))"
                  >
                    {{ show(totals[r.key]) }}
                  </dd>
                </div>
              </dl>
            </div>
            <div
              class="flex flex-col gap-2 border-t border-border-subtle pt-4 md:border-t-0 md:border-l md:pt-0 md:pl-6"
            >
              <p id="delivery-remaining-title" class="text-overline text-ink-500" lang="tr">
                {{ t('tracking.remaining') }}
              </p>
              <dl
                data-testid="delivery-remaining"
                aria-labelledby="delivery-remaining-title"
                class="grid grid-cols-2 gap-4"
              >
                <div v-for="r in REMAINING" :key="r.key" class="flex flex-col gap-0.5">
                  <dt class="flex items-center gap-1.5 text-body-sm text-ink-600">
                    <span :class="cn('size-2 shrink-0 rounded-full', r.dot)" aria-hidden="true" />
                    {{ t(`status.delivery.${r.key}`) }}
                  </dt>
                  <dd
                    :data-testid="`delivery-count-total-${r.key}`"
                    :data-value="dv(totals[r.key])"
                    :class="cn('text-numeral-sm tabular-nums', valueClass(r.key, totals[r.key]))"
                  >
                    {{ show(totals[r.key]) }}
                  </dd>
                </div>
              </dl>
            </div>
          </div>

          <div
            v-if="updatedText || (state === 'completed' && totals.failed > 0)"
            class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-border-subtle pt-3"
          >
            <p
              v-if="updatedText"
              data-testid="delivery-last-updated"
              :data-value="lastUpdatedAt ?? undefined"
              class="flex items-center gap-1.5 text-caption text-ink-500"
            >
              <Clock class="size-3.5 shrink-0" aria-hidden="true" />
              {{ updatedText }}
            </p>
            <a
              v-if="state === 'completed' && totals.failed > 0"
              data-testid="delivery-failed-link"
              :href="failedHref"
              class="inline-flex w-fit items-center gap-1.5 text-body-sm font-medium text-error underline underline-offset-4 max-lg:min-h-11 hover:text-error-strong"
              @click="openFailed"
              >{{ t('delivery.failedLink') }} ({{ formatNumber(totals.failed) }})<ArrowRight
                class="size-4"
                aria-hidden="true"
            /></a>
          </div>
        </div>
      </div>

      <!-- Per channel (DESIGN.md §8.5): side by side ≥768 px, stacked below. -->
      <div class="grid gap-4 md:grid-cols-2">
        <article
          v-for="c in channels"
          :key="c.key"
          :data-testid="`delivery-card-${c.key}`"
          :aria-labelledby="`delivery-card-${c.key}-title`"
          class="flex min-w-0 flex-col gap-4 rounded-card border border-border-default bg-surface-card p-4 shadow-card md:p-6"
        >
          <div class="flex items-center justify-between gap-3">
            <h3
              :id="`delivery-card-${c.key}-title`"
              class="flex items-center gap-2 text-h3 text-accent-heading"
            >
              <span :class="cn('size-2.5 shrink-0 rounded-full', c.dot)" aria-hidden="true" />
              {{ c.label }}
            </h3>
            <p
              :data-testid="`delivery-percent-${c.key}`"
              :data-value="dv(percent(c.counts))"
              class="text-numeral-md text-accent-numeral tabular-nums"
            >
              {{ status ? `%${percent(c.counts)}` : '—' }}
            </p>
          </div>

          <div class="flex flex-col gap-2">
            <p class="text-body-sm text-ink-600">
              <template v-for="(part, i) in CHANNEL_LINE" :key="i"
                ><span
                  v-if="part.key === 'total'"
                  :data-testid="`delivery-count-${c.key}-total`"
                  :data-value="dv(c.counts.total)"
                  class="text-numeral-md text-ink-900 tabular-nums"
                  >{{ show(c.counts.total) }}</span
                ><span v-else-if="part.key === 'done'" class="tabular-nums">{{
                  show(finished(c.counts))
                }}</span
                ><template v-else>{{ part.text }}</template></template
              >
            </p>
            <ProgressBar
              :data-testid="`delivery-bar-${c.key}`"
              :value="finished(c.counts)"
              :max="c.counts.total"
              variant="segmented"
              size="md"
              :segments="c.counts"
              :label="
                t('delivery.barLabel', {
                  channel: c.label,
                  done: formatNumber(finished(c.counts)),
                  total: formatNumber(c.counts.total),
                })
              "
            />
          </div>

          <dl class="flex flex-col divide-y divide-border-subtle">
            <div
              v-for="r in ROWS"
              :key="r.key"
              class="flex items-baseline justify-between gap-3 py-2"
            >
              <dt class="flex items-center gap-2 text-body-sm text-ink-600">
                <span :class="cn('size-2 shrink-0 rounded-full', r.dot)" aria-hidden="true" />
                {{ t(`status.delivery.${r.key}`) }}
              </dt>
              <dd
                :data-testid="`delivery-count-${c.key}-${r.key}`"
                :data-value="dv(c.counts[r.key])"
                :class="cn('text-numeral-sm tabular-nums', valueClass(r.key, c.counts[r.key]))"
              >
                {{ show(c.counts[r.key]) }}
              </dd>
            </div>
          </dl>
        </article>
      </div>
    </template>
  </section>
</template>
