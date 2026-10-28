<script setup lang="ts">
// ui.md §3.8: human-readable chips + read-only JSON. Rule values are data, shown as text only.
import { Braces, ListFilter } from '@lucide/vue'
import { computed, ref, useId } from 'vue'

import type { MessageId } from '@/i18n'
import { formatNumber, hasMessage, t } from '@/i18n'

const props = defineProps<{ rule: object }>()

interface Chip {
  field: string
  op: string
  text: string
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null

const label = (prefix: string, key: string) => {
  const id = `${prefix}.${key}`
  return hasMessage(id) ? t(id as MessageId) : key
}

function formatValue(v: unknown): string {
  if (v === undefined || v === null) return ''
  if (typeof v === 'number') return formatNumber(v)
  if (typeof v === 'string') return v
  if (Array.isArray(v)) return v.map(formatValue).join(', ')
  if (isObj(v)) {
    if (typeof v.days === 'number') return t('rule.days', { n: formatNumber(v.days) })
    if ('min' in v || 'max' in v) {
      return t('rule.range', { min: formatValue(v.min), max: formatValue(v.max) })
    }
  }
  return JSON.stringify(v)
}

function collect(node: unknown, negated: boolean, inAny: boolean, out: Chip[]) {
  if (!isObj(node)) return
  if (Array.isArray(node.all)) {
    for (const n of node.all) collect(n, negated, false, out)
    return
  }
  if (Array.isArray(node.any)) {
    for (const n of node.any) collect(n, negated, true, out)
    return
  }
  if ('not' in node) {
    collect(node.not, !negated, inAny, out)
    return
  }
  if ('field' in node || 'op' in node) {
    const field = String(node.field ?? '')
    const op = String(node.op ?? '')
    const parts = [label('rule.field', field), label('rule.op', op), formatValue(node.value)]
    let text = parts.filter(Boolean).join(' ')
    if (negated) text = `${t('rule.not')}: ${text}`
    if (inAny) text = `${t('rule.anyGroup')}: ${text}`
    out.push({ field, op, text })
  }
}

const chips = computed(() => {
  const out: Chip[] = []
  collect(props.rule, false, false, out)
  return out
})
const json = computed(() => JSON.stringify(props.rule, null, 2))

const showJson = ref(false)
const jsonId = useId()
</script>

<template>
  <div data-testid="segment-rule" class="flex flex-col gap-3">
    <div class="flex flex-wrap items-center gap-2">
      <ListFilter class="size-4 text-ink-500" aria-hidden="true" />
      <ul class="flex flex-wrap gap-2">
        <li
          v-for="(c, i) in chips"
          :key="i"
          data-testid="segment-rule-chip"
          :data-field="c.field"
          :data-op="c.op"
          class="rounded-pill border border-brand-line bg-accent px-3 py-1 text-body-sm text-ink-700"
        >
          {{ c.text }}
        </li>
      </ul>
      <button
        type="button"
        data-testid="segment-rule-json-toggle"
        :aria-expanded="showJson ? 'true' : 'false'"
        :aria-controls="jsonId"
        class="ml-auto inline-flex min-h-8 items-center max-lg:min-h-11 gap-1 rounded-pill px-3 text-body-sm font-medium text-accent-text hover:bg-accent hover:text-accent-hover"
        @click="showJson = !showJson"
      >
        <Braces class="size-4" aria-hidden="true" />
        {{ showJson ? t('rule.hideJson') : t('rule.showJson') }}
      </button>
    </div>
    <pre
      v-if="showJson"
      :id="jsonId"
      data-testid="segment-rule-json"
      class="max-h-72 overflow-auto rounded-sm bg-surface-sunken p-4 font-mono text-body-sm text-ink-700"
      >{{ json }}</pre>
  </div>
</template>
