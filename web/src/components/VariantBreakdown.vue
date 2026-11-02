<script setup lang="ts">
// ui.md §3.30 (REQ-ABT-04, D64): deliveries per A/B variant on the tracking step. Delivery
// outcome only: the mock provider measures no opens or clicks, so there is no winner, no ratio
// and no highlight of either row (owner decision). Angles are text nodes, never v-html.
// P10 (ui.md §3.30): below 640 px the table is hidden and an extra card view (`variant-cards`, one
// card per variant) shows the same numbers; the table stays in the DOM, and the cards carry no
// `variant-` testid so every such testid stays unique.
import { FlaskConical, Info } from '@lucide/vue'

import type { VariantStatusCounts } from '@/api/types'
import { formatNumber, t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { ContentVariant } from '@/lib/variant'

defineProps<{
  byVariant: VariantStatusCounts
  angles: { a: string; b: string } | null
}>()

const VARIANTS: readonly ContentVariant[] = ['a', 'b']
const CHANNELS = [
  { key: 'sms', label: 'tabs.sms', dot: 'bg-sms' },
  { key: 'email', label: 'tabs.email', dot: 'bg-email' },
] as const
const COUNTS = [
  { key: 'sent', label: 'tracking.sent' },
  { key: 'failed', label: 'tracking.failed' },
  { key: 'queued', label: 'tracking.queued' },
] as const

/** Number colour rule shared with the counters (ui.md §3.20, P10). */
const countClass = (k: 'sent' | 'failed' | 'queued', n: number) =>
  k === 'sent'
    ? 'text-accent-numeral'
    : k === 'failed'
      ? n > 0
        ? 'text-error'
        : 'text-ink-900'
      : 'text-ink-700'
</script>

<template>
  <section
    class="flex flex-col gap-3 rounded-card border border-border-default bg-surface-card p-4 shadow-card md:p-6"
  >
    <!-- ≥640 px: the table (scrolls sideways inside its own box up to 768 px). -->
    <div class="overflow-x-auto max-sm:hidden">
      <table data-testid="variant-breakdown" class="w-full min-w-[40rem] text-left text-body-sm">
        <caption class="mb-3 text-left">
          <span class="flex items-center gap-2 text-h4 text-accent-heading">
            <FlaskConical class="size-5 text-brand-icon" aria-hidden="true" />
            {{ t('tracking.variantTitle') }}
          </span>
        </caption>
        <thead class="text-caption text-ink-500">
          <tr>
            <th
              scope="col"
              rowspan="2"
              class="sticky left-0 z-10 bg-surface-card py-2 pr-4 align-bottom font-medium"
            >
              {{ t('tracking.variantCol') }}
            </th>
            <th scope="col" rowspan="2" class="py-2 pr-4 align-bottom font-medium">
              {{ t('tracking.angleCol') }}
            </th>
            <th
              v-for="c in CHANNELS"
              :key="c.key"
              scope="colgroup"
              colspan="3"
              class="border-l border-border-subtle px-3 pt-2 pb-1 font-semibold text-ink-700"
            >
              <span class="inline-flex items-center gap-1.5">
                <span :class="cn('size-2 rounded-full', c.dot)" aria-hidden="true" />
                {{ t(c.label) }}
              </span>
            </th>
          </tr>
          <tr class="border-b border-border-default">
            <template v-for="c in CHANNELS" :key="c.key">
              <th
                v-for="(k, i) in COUNTS"
                :key="`${c.key}-${k.key}`"
                scope="col"
                :class="
                  cn(
                    'px-3 pt-1 pb-2 text-right font-medium whitespace-nowrap',
                    i === 0 && 'border-l border-border-subtle',
                  )
                "
              >
                {{ t(k.label) }}
              </th>
            </template>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="v in VARIANTS"
            :key="v"
            :data-testid="`variant-row-${v}`"
            class="border-b border-border-subtle last:border-b-0"
          >
            <th
              scope="row"
              class="sticky left-0 z-10 bg-surface-card py-3 pr-4 font-semibold whitespace-nowrap text-ink-900"
            >
              <span class="inline-flex items-center gap-2">
                <span
                  aria-hidden="true"
                  :data-mark="v.toUpperCase()"
                  class="inline-flex size-5 items-center justify-center rounded-sm border border-border-default font-mono text-caption leading-none text-ink-600 before:content-[attr(data-mark)]"
                />
                {{ t(v === 'a' ? 'variant.a' : 'variant.b') }}
              </span>
            </th>
            <td
              :data-testid="`variant-angle-${v}`"
              class="max-w-[16rem] min-w-40 py-3 pr-4 break-words text-ink-700"
              v-text="angles ? angles[v] : '—'"
            />
            <template v-for="c in CHANNELS" :key="c.key">
              <td
                v-for="(k, i) in COUNTS"
                :key="`${c.key}-${k.key}`"
                :data-testid="`variant-count-${v}-${c.key}-${k.key}`"
                :data-value="byVariant[v][c.key][k.key]"
                :class="
                  cn(
                    'px-3 py-3 text-right font-mono tabular-nums',
                    i === 0 && 'border-l border-border-subtle',
                    countClass(k.key, byVariant[v][c.key][k.key]),
                  )
                "
              >
                {{ formatNumber(byVariant[v][c.key][k.key]) }}
              </td>
            </template>
          </tr>
        </tbody>
      </table>
    </div>
    <!-- <640 px: one card per variant instead of a sideways-scrolling table. -->
    <div
      data-testid="variant-cards"
      role="group"
      aria-labelledby="variant-cards-title"
      class="flex flex-col gap-3 sm:hidden"
    >
      <p id="variant-cards-title" class="flex items-center gap-2 text-h4 text-accent-heading">
        <FlaskConical class="size-5 text-brand-icon" aria-hidden="true" />
        {{ t('tracking.variantTitle') }}
      </p>
      <article
        v-for="v in VARIANTS"
        :key="v"
        :aria-label="t(v === 'a' ? 'variant.a' : 'variant.b')"
        class="flex flex-col gap-3 rounded-card border border-border-subtle bg-surface-sunken p-3"
      >
        <p class="flex items-center gap-2 text-body-sm font-semibold text-ink-900">
          <span
            aria-hidden="true"
            :data-mark="v.toUpperCase()"
            class="inline-flex size-5 items-center justify-center rounded-sm border border-border-default bg-surface-card font-mono text-caption leading-none text-ink-600 before:content-[attr(data-mark)]"
          />
          {{ t(v === 'a' ? 'variant.a' : 'variant.b') }}
        </p>
        <p class="text-body-sm break-words text-ink-700">
          <span class="sr-only">{{ t('tracking.angleCol') }}: </span>{{ angles ? angles[v] : '—' }}
        </p>
        <div
          v-for="c in CHANNELS"
          :key="c.key"
          class="flex flex-col gap-1.5 border-t border-border-subtle pt-2"
        >
          <p class="flex items-center gap-1.5 text-caption font-semibold text-ink-700">
            <span :class="cn('size-2 rounded-full', c.dot)" aria-hidden="true" />
            {{ t(c.label) }}
          </p>
          <dl class="grid grid-cols-3 gap-2">
            <div v-for="k in COUNTS" :key="k.key" class="flex flex-col gap-0.5">
              <dt class="text-caption text-ink-600">{{ t(k.label) }}</dt>
              <dd
                :class="
                  cn('text-numeral-sm tabular-nums', countClass(k.key, byVariant[v][c.key][k.key]))
                "
              >
                {{ formatNumber(byVariant[v][c.key][k.key]) }}
              </dd>
            </div>
          </dl>
        </div>
      </article>
    </div>
    <p
      data-testid="variant-breakdown-note"
      role="note"
      class="flex items-start gap-2 text-caption text-ink-500"
    >
      <Info class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      {{ t('tracking.variantNote') }}
    </p>
  </section>
</template>
