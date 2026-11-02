<script setup lang="ts">
// ui.md §3.31 / DESIGN.md §8.1 (REQ-DSH-02, D65): the overview under the brief composer on `/`.
// Five summary numbers (counts only, no ratios) and two fixed sections, Planned and Sent, five rows
// each with "show more". Each section loads, fails and retries on its own; the composer never waits
// for the panel. One setTimeout refreshes both sections quietly: every 10 s while a shown row is
// sending, and once 10 s after the earliest plan within the hour starts. No per-campaign stream.
import { CalendarClock, ChevronDown, CircleAlert, RotateCcw } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { toApiError } from '@/api/errors'
import type { CampaignOverview, CampaignSummary, CampaignSummaryPage } from '@/api/types'
import type { MessageId } from '@/i18n'
import { formatNumber, messageParts, t } from '@/i18n'
import { formatTsiPlain, remainingSeconds } from '@/lib/schedule'
import { injectApiClient } from '@/stores/api'

import Button from './Button.vue'
import ProgressBar from './ProgressBar.vue'
import Skeleton from './Skeleton.vue'
import StatusBadge from './StatusBadge.vue'

type SectionKey = 'scheduled' | 'sent'

const PAGE = 5
const MAX_LIMIT = 200
const SENDING_REFRESH_MS = 10_000
/** A plan starting within this many seconds schedules one refresh 10 s after its start. */
const PLAN_HORIZON_S = 3600

const QUERY: Record<SectionKey, { state: string; order: 'scheduled_at' | 'recent' }> = {
  scheduled: { state: 'scheduled', order: 'scheduled_at' },
  sent: { state: 'sending,completed', order: 'recent' },
}

interface Section {
  items: CampaignSummary[]
  total: number
  /** At least one successful answer (initial, retry, more or refresh). */
  loaded: boolean
  /** The initial (or retried initial) request is in flight. */
  loading: boolean
  loadingMore: boolean
  error: ApiError | null
  /** What the retry button repeats: the failed initial load or the failed "more" request. */
  failed: { offset: number } | null
  /** remainingSeconds at the moment each planned row was loaded (no per-second update). */
  relative: Record<string, number>
}

const newSection = (): Section => ({
  items: [],
  total: 0,
  loaded: false,
  loading: true,
  loadingMore: false,
  error: null,
  failed: null,
  relative: {},
})

const client = injectApiClient()

const sections = reactive<Record<SectionKey, Section>>({
  scheduled: newSection(),
  sent: newSection(),
})
const overview = ref<CampaignOverview | null>(null)
const announcement = ref('')

/** Bumped by every request that writes a section; an older answer for it is dropped. */
const generation: Record<SectionKey, number> = { scheduled: 0, sent: 0 }
let timer: ReturnType<typeof setTimeout> | null = null
let disposed = false
/** The timer fired while a "show more" request was in flight; re-armed when it settles. */
let deferredRefresh = false

// ---- data-state, summary numbers ----
const sectionState = (s: Section) => {
  if (s.loading && !s.loaded) return 'loading'
  if (s.error && s.items.length === 0 && !s.loaded) return 'error'
  if (s.loaded && s.total === 0) return 'empty'
  if (s.loaded) return 'ready'
  return s.error ? 'error' : 'loading'
}

const KPIS: { key: string; label: MessageId; value: (o: CampaignOverview) => number }[] = [
  { key: 'total', label: 'briefPanel.kpi.total', value: (o) => o.campaigns.total },
  { key: 'scheduled', label: 'briefPanel.kpi.scheduled', value: (o) => o.campaigns.scheduled },
  {
    key: 'sent',
    label: 'briefPanel.kpi.sent',
    value: (o) => o.campaigns.sending + o.campaigns.completed,
  },
  { key: 'messages-sent', label: 'briefPanel.kpi.messagesSent', value: (o) => o.deliveries.sent },
  {
    key: 'messages-failed',
    label: 'briefPanel.kpi.messagesFailed',
    value: (o) => o.deliveries.failed,
  },
]

const kpis = computed(() =>
  KPIS.map((k) => {
    const n = overview.value ? k.value(overview.value) : null
    return {
      ...k,
      n,
      text: n === null ? '—' : formatNumber(n),
      tone:
        k.key === 'messages-failed' && n !== null && n > 0 ? 'text-error' : 'text-accent-numeral',
    }
  }),
)

const SECTION_META: Record<SectionKey, { title: MessageId; empty: MessageId; error: MessageId }> = {
  scheduled: {
    title: 'briefPanel.scheduledTitle',
    empty: 'briefPanel.scheduledEmpty',
    error: 'briefPanel.scheduledError',
  },
  sent: {
    title: 'briefPanel.sentTitle',
    empty: 'briefPanel.sentEmpty',
    error: 'briefPanel.sentError',
  },
}
const SECTION_KEYS: SectionKey[] = ['scheduled', 'sent']

/** The zone is said once for the whole panel, only while some time is on screen. */
const showsTime = computed(
  () =>
    sections.scheduled.items.some((i) => i.scheduled_at) ||
    sections.sent.items.some((i) => i.sending_started_at),
)

const SENT_COUNTS = messageParts('briefPanel.sentCounts')
const firstLine = (brief: string) => brief.split(/\r?\n/)[0] ?? ''
const deliveriesOf = (i: CampaignSummary) =>
  i.deliveries ?? { total: 0, queued: 0, sending: 0, sent: 0, failed: 0 }

const relativeFormat = new Intl.RelativeTimeFormat('tr-TR', { numeric: 'auto' })
/** "2 saat sonra" from the seconds left at load time; coarse on purpose (no live countdown). */
function relativeText(seconds: number): string {
  if (seconds < 60) return relativeFormat.format(Math.round(seconds), 'second')
  if (seconds < 3600) return relativeFormat.format(Math.round(seconds / 60), 'minute')
  if (seconds < 86_400) return relativeFormat.format(Math.round(seconds / 3600), 'hour')
  return relativeFormat.format(Math.round(seconds / 86_400), 'day')
}

const trackingTo = (id: string) => ({ name: 'tracking', params: { campaignId: id } })

// ---- requests ----
function fetchPage(key: SectionKey, limit: number, offset: number) {
  return api.listCampaignSummaries(client, { ...QUERY[key], limit, offset })
}

function recordRelative(s: Section, items: CampaignSummary[]) {
  const now = Date.now()
  for (const i of items) {
    if (i.scheduled_at) s.relative[i.id] = remainingSeconds(i.scheduled_at, now)
  }
}

/** A successful answer: the summary numbers follow it and the refresh timer is set again. */
function accepted(page: CampaignSummaryPage) {
  overview.value = page.overview
  schedule()
}

async function loadInitial(key: SectionKey) {
  const s = sections[key]
  const gen = ++generation[key]
  s.loading = true
  s.error = null
  s.failed = null
  try {
    const page = await fetchPage(key, PAGE, 0)
    if (disposed || gen !== generation[key]) return
    s.items = page.items
    s.total = page.total
    s.loaded = true
    recordRelative(s, page.items)
    accepted(page)
  } catch (e) {
    if (disposed || gen !== generation[key]) return
    s.error = toApiError(e)
    s.failed = { offset: 0 }
  } finally {
    if (gen === generation[key]) s.loading = false
  }
}

const itemRefs = new Map<string, HTMLElement>()
/** Row refs are RouterLink instances; focus goes to their rendered <a>. */
function setItemRef(id: string, ref: unknown) {
  const el = (ref as { $el?: unknown } | null)?.$el
  if (el instanceof HTMLElement) itemRefs.set(id, el)
  else itemRefs.delete(id)
}

async function loadMore(key: SectionKey, offset = sections[key].items.length) {
  const s = sections[key]
  if (s.loadingMore) return
  const gen = ++generation[key]
  s.loadingMore = true
  s.error = null
  s.failed = null
  try {
    const page = await fetchPage(key, PAGE, offset)
    if (disposed || gen !== generation[key]) return
    const shown = new Set(s.items.map((i) => i.id))
    const added = page.items.filter((i) => !shown.has(i.id))
    s.items = [...s.items, ...added]
    s.total = page.total
    s.loaded = true
    recordRelative(s, added)
    s.loadingMore = false
    accepted(page)
    const first = added[0]
    if (first) {
      announcement.value = ''
      await nextTick()
      itemRefs.get(first.id)?.focus()
      announcement.value = t('briefPanel.moreLoaded', { n: added.length })
    }
  } catch (e) {
    if (disposed || gen !== generation[key]) return
    s.error = toApiError(e)
    s.failed = { offset }
  } finally {
    // Also when superseded: the button must never stay in its loading state.
    s.loadingMore = false
    // A refresh skipped while this request ran is owed now, whatever the outcome (a failed
    // "more" would otherwise leave sending rows without any timer).
    if (
      deferredRefresh &&
      !disposed &&
      !sections.scheduled.loadingMore &&
      !sections.sent.loadingMore
    ) {
      deferredRefresh = false
      schedule()
    }
  }
}

function retry(key: SectionKey) {
  const f = sections[key].failed
  if (!f || f.offset === 0) void loadInitial(key)
  else void loadMore(key, f.offset)
}

/** Quiet in-place refresh: no skeleton, no announcement, errors ignored (rows stay). */
async function refresh(key: SectionKey) {
  const s = sections[key]
  const gen = ++generation[key]
  const limit = Math.min(MAX_LIMIT, Math.max(PAGE, s.items.length))
  try {
    const page = await fetchPage(key, limit, 0)
    if (disposed || gen !== generation[key]) return
    s.items = page.items
    s.total = page.total
    s.loaded = true
    s.loading = false
    s.error = null
    s.failed = null
    recordRelative(s, page.items)
    accepted(page)
  } catch (e) {
    // Silent by contract: the rows stay and no timer is set from a failed refresh. Only a section
    // that never loaded (its first request was superseded by this one) shows the error, so it
    // does not keep a skeleton forever.
    if (disposed || gen !== generation[key] || s.loaded) return
    s.loading = false
    s.error = toApiError(e)
    s.failed = { offset: 0 }
  }
}

// ---- the one refresh timer (ui.md §3.31) ----
function nextDelay(): number | null {
  const candidates: number[] = []
  const rows = [...sections.scheduled.items, ...sections.sent.items]
  if (rows.some((i) => i.state === 'sending')) candidates.push(SENDING_REFRESH_MS)
  const now = Date.now()
  let earliest: number | null = null
  for (const i of sections.scheduled.items) {
    if (!i.scheduled_at) continue
    const r = remainingSeconds(i.scheduled_at, now)
    if (earliest === null || r < earliest) earliest = r
  }
  if (earliest !== null && earliest <= PLAN_HORIZON_S) {
    candidates.push(earliest * 1000 + SENDING_REFRESH_MS)
  }
  return candidates.length ? Math.min(...candidates) : null
}

function clearTimer() {
  if (timer !== null) clearTimeout(timer)
  timer = null
}

function schedule() {
  clearTimer()
  // A hidden tab polls nothing; visibilitychange re-arms the timer when it is shown again.
  if (disposed || document.hidden) return
  const delay = nextDelay()
  if (delay === null) return
  timer = setTimeout(onTimer, delay)
}

function onTimer() {
  timer = null
  if (disposed) return
  // A "show more" in flight re-arms the timer when it settles (success or failure).
  if (sections.scheduled.loadingMore || sections.sent.loadingMore) {
    deferredRefresh = true
    return
  }
  void refresh('scheduled')
  void refresh('sent')
}

function onVisibility() {
  if (document.hidden) clearTimer()
  else schedule()
}

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibility)
  void loadInitial('scheduled')
  void loadInitial('sent')
})

onBeforeUnmount(() => {
  disposed = true
  document.removeEventListener('visibilitychange', onVisibility)
  clearTimer()
})
</script>

<template>
  <section
    data-testid="brief-campaign-panel"
    aria-labelledby="brief-panel-title"
    class="flex flex-col gap-4"
  >
    <h2 id="brief-panel-title" class="text-h2 text-accent-heading">{{ t('briefPanel.title') }}</h2>

    <!-- Summary numbers: counts only (no ratios), small and shadowless; the page's job is the brief. -->
    <dl class="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-5">
      <div
        v-for="(k, i) in kpis"
        :key="k.key"
        :class="[
          'flex min-w-0 flex-col gap-1 rounded-card border border-border-default bg-surface-card px-4 py-3',
          i === kpis.length - 1 && 'max-md:col-span-2',
        ]"
      >
        <dt class="text-caption text-ink-600">{{ t(k.label) }}</dt>
        <dd
          :data-testid="`campaign-kpi-${k.key}`"
          :data-value="k.n ?? undefined"
          :class="['text-numeral-md tabular-nums', k.n === null ? 'text-ink-500' : k.tone]"
        >
          {{ k.text }}
        </dd>
      </div>
    </dl>

    <div class="grid gap-4 lg:grid-cols-12 lg:items-start">
      <section
        v-for="key in SECTION_KEYS"
        :key="key"
        :data-testid="`brief-${key}-summary`"
        :data-state="sectionState(sections[key])"
        :aria-labelledby="`brief-${key}-title`"
        :aria-busy="sectionState(sections[key]) === 'loading' ? 'true' : undefined"
        :class="[
          'flex min-w-0 flex-col gap-2 rounded-card border border-border-default bg-surface-card p-3 md:p-4',
          key === 'scheduled' ? 'lg:col-span-5' : 'lg:col-span-7',
        ]"
      >
        <h3 :id="`brief-${key}-title`" class="px-3 pt-1 text-h4 text-accent-heading">
          {{ t(SECTION_META[key].title)
          }}<span
            v-if="sections[key].loaded"
            :data-testid="`brief-${key}-total`"
            :data-value="sections[key].total"
            class="font-normal text-ink-500 tabular-nums"
            >{{ ` · ${formatNumber(sections[key].total)}` }}</span
          >
        </h3>

        <!-- First load: row-shaped placeholders (no spinner). -->
        <div v-if="sectionState(sections[key]) === 'loading'" class="flex flex-col gap-4 px-3 py-3">
          <span class="sr-only">{{ t('briefPanel.loading') }}</span>
          <div v-for="n in 2" :key="n" class="flex flex-col gap-2">
            <Skeleton class="h-5 w-4/5" />
            <Skeleton class="h-3.5 w-1/2" />
          </div>
        </div>

        <p
          v-else-if="sectionState(sections[key]) === 'empty'"
          :data-testid="`brief-${key}-empty`"
          class="px-3 pt-1 pb-3 text-body-sm text-ink-600"
        >
          {{ t(SECTION_META[key].empty) }}
        </p>

        <ul v-if="sections[key].items.length" :data-testid="`brief-${key}-list`">
          <li
            v-for="item in sections[key].items"
            :key="item.id"
            class="border-b border-border-subtle last:border-b-0"
          >
            <RouterLink
              :ref="(el) => setItemRef(item.id, el)"
              data-testid="campaign-summary-item"
              :to="trackingTo(item.id)"
              :data-campaign-id="item.id"
              :data-state="item.state"
              class="flex flex-col gap-1.5 rounded-sm px-3 py-3 text-ink-900 no-underline transition-colors duration-[var(--dur-fast)] hover:bg-surface-sunken hover:text-ink-900 max-lg:min-h-11"
            >
              <!-- Planned row: brief, then the planned time and how far away it was at load. -->
              <template v-if="key === 'scheduled'">
                <span
                  data-testid="campaign-summary-brief"
                  class="truncate text-body text-ink-900 max-sm:line-clamp-2 max-sm:whitespace-normal"
                  v-text="firstLine(item.brief)"
                />
                <span
                  v-if="item.scheduled_at"
                  class="flex flex-wrap items-center gap-x-1.5 text-caption text-ink-600"
                >
                  <CalendarClock class="size-3.5 shrink-0 text-info" aria-hidden="true" />
                  <span
                    data-testid="campaign-summary-time"
                    :data-value="item.scheduled_at"
                    class="tabular-nums"
                    >{{ formatTsiPlain(item.scheduled_at) }}</span
                  >
                  <span aria-hidden="true">·</span>
                  <span
                    data-testid="campaign-summary-relative"
                    :data-value="sections.scheduled.relative[item.id] ?? 0"
                    class="font-semibold text-ink-700"
                    >{{ relativeText(sections.scheduled.relative[item.id] ?? 0) }}</span
                  >
                </span>
              </template>

              <!-- Sent row: brief + state, a thin bar, the outcome and when sending started. -->
              <template v-else>
                <span class="flex items-start justify-between gap-3">
                  <span
                    data-testid="campaign-summary-brief"
                    class="min-w-0 truncate text-body text-ink-900 max-sm:line-clamp-2 max-sm:whitespace-normal"
                    v-text="firstLine(item.brief)"
                  />
                  <StatusBadge
                    data-testid="campaign-summary-state"
                    kind="campaign"
                    :status="item.state"
                    size="sm"
                    class="mt-0.5 shrink-0"
                  />
                </span>
                <ProgressBar
                  data-testid="campaign-summary-bar"
                  :value="deliveriesOf(item).sent + deliveriesOf(item).failed"
                  :max="deliveriesOf(item).total"
                  variant="segmented"
                  size="sm"
                  :segments="deliveriesOf(item)"
                />
                <span
                  class="flex flex-wrap items-center justify-between gap-x-3 gap-y-0.5 text-caption text-ink-600"
                >
                  <span class="tabular-nums">
                    <template v-for="(part, j) in SENT_COUNTS" :key="j"
                      ><span
                        v-if="part.key === 'sent'"
                        data-testid="campaign-summary-sent"
                        :data-value="deliveriesOf(item).sent"
                        class="font-semibold text-ink-700"
                        >{{ formatNumber(deliveriesOf(item).sent) }}</span
                      ><span
                        v-else-if="part.key === 'failed'"
                        data-testid="campaign-summary-failed"
                        :data-value="deliveriesOf(item).failed"
                        :class="[
                          'font-semibold',
                          deliveriesOf(item).failed > 0 ? 'text-error' : 'text-ink-700',
                        ]"
                        >{{ formatNumber(deliveriesOf(item).failed) }}</span
                      ><template v-else>{{ part.text }}</template></template
                    >
                  </span>
                  <span
                    v-if="item.sending_started_at"
                    data-testid="campaign-summary-time"
                    :data-value="item.sending_started_at"
                    class="tabular-nums"
                    >{{ formatTsiPlain(item.sending_started_at) }}</span
                  >
                </span>
              </template>
            </RouterLink>
          </li>
        </ul>

        <div
          v-if="sections[key].error"
          class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 px-3 py-2"
        >
          <p
            :data-testid="`brief-${key}-error`"
            :data-code="sections[key].error?.code"
            class="flex items-start gap-1.5 text-body-sm text-error"
          >
            <CircleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
            <span>{{ t(SECTION_META[key].error) }}</span>
          </p>
          <Button :data-testid="`brief-${key}-retry`" variant="ghost" size="sm" @click="retry(key)">
            <RotateCcw aria-hidden="true" />
            {{ t('briefPanel.retry') }}
          </Button>
        </div>

        <div
          v-if="sections[key].loaded && sections[key].items.length < sections[key].total"
          class="px-3 pt-1 pb-1"
        >
          <Button
            :data-testid="`brief-${key}-more`"
            variant="secondary"
            size="sm"
            :loading="sections[key].loadingMore"
            class="w-full sm:w-auto"
            @click="loadMore(key)"
          >
            <ChevronDown v-if="!sections[key].loadingMore" aria-hidden="true" />
            {{ t('briefPanel.more') }}
          </Button>
        </div>
      </section>
    </div>

    <p v-if="showsTime" data-testid="brief-panel-tz-note" class="text-caption text-ink-500">
      {{ t('schedule.tzNote') }}
    </p>
    <p data-testid="brief-panel-status" role="status" class="sr-only">{{ announcement }}</p>
  </section>
</template>
