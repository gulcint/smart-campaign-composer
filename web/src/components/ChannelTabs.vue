<script setup lang="ts">
// ui.md §3.9: SMS / Email tabs following the WAI-ARIA tabs pattern (same contract as Reka Tabs:
// roving tabindex, automatic activation, wrap-around, Home/End). Hand-rolled because the
// contract needs aria-controls and the roving tabindex to be correct from the first render.
// Both panels stay in the DOM; the unselected one is `hidden`, so streaming keeps filling it.
import { CircleAlert } from '@lucide/vue'
import { computed, nextTick, ref, useId } from 'vue'

import type { ChannelKey, ContentPhase, FieldKey } from '@/api/types'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const props = defineProps<{
  modelValue: ChannelKey
  phases: Record<FieldKey, ContentPhase>
}>()
const emit = defineEmits<{ 'update:modelValue': [channel: ChannelKey] }>()

const CHANNELS: readonly ChannelKey[] = ['sms', 'email']
const FIELDS_OF: Record<ChannelKey, readonly FieldKey[]> = {
  sms: ['sms-body'],
  email: ['email-subject', 'email-body'],
}

const baseId = useId()
const tabId = (c: ChannelKey) => `${baseId}-tab-${c}`
const panelId = (c: ChannelKey) => `${baseId}-panel-${c}`
const tabEls = ref<Partial<Record<ChannelKey, HTMLElement>>>({})

/** Channel summary: invalid if any field is invalid, otherwise the first field's phase. */
const status = computed<Record<ChannelKey, ContentPhase>>(() => {
  const of = (c: ChannelKey): ContentPhase => {
    const fields = FIELDS_OF[c]
    if (fields.some((f) => props.phases[f] === 'invalid')) return 'invalid'
    return props.phases[fields[0] as FieldKey]
  }
  return { sms: of('sms'), email: of('email') }
})

function select(c: ChannelKey, focus = false) {
  if (c !== props.modelValue) emit('update:modelValue', c)
  if (focus) void nextTick(() => tabEls.value[c]?.focus())
}

function onKeydown(e: KeyboardEvent, from: ChannelKey) {
  const i = CHANNELS.indexOf(from)
  const n = CHANNELS.length
  let to: ChannelKey | undefined
  if (e.key === 'ArrowRight') to = CHANNELS[(i + 1) % n]
  else if (e.key === 'ArrowLeft') to = CHANNELS[(i - 1 + n) % n]
  else if (e.key === 'Home') to = CHANNELS[0]
  else if (e.key === 'End') to = CHANNELS[n - 1]
  if (!to) return
  e.preventDefault()
  select(to, true)
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div
      role="tablist"
      :aria-label="t('tabs.channels')"
      class="inline-flex w-fit gap-1 rounded-pill border border-border-default bg-surface-card p-1"
    >
      <button
        v-for="c in CHANNELS"
        :id="tabId(c)"
        :key="c"
        :ref="(el) => (tabEls[c] = (el as HTMLElement | null) ?? undefined)"
        type="button"
        role="tab"
        :data-testid="`channel-tab-${c}`"
        :data-status="status[c]"
        :data-state="c === modelValue ? 'active' : 'inactive'"
        :aria-selected="c === modelValue ? 'true' : 'false'"
        :aria-controls="panelId(c)"
        :tabindex="c === modelValue ? 0 : -1"
        :class="
          cn(
            'inline-flex h-9 items-center gap-2 rounded-pill px-4 text-body-sm font-semibold text-ink-600',
            'transition-colors outline-none focus-visible:shadow-focus',
            'data-[state=active]:bg-secondary data-[state=active]:text-accent-text',
          )
        "
        @click="select(c)"
        @keydown="onKeydown($event, c)"
      >
        <span
          :class="cn('size-2 rounded-full', c === 'sms' ? 'bg-sms' : 'bg-email')"
          aria-hidden="true"
        />
        {{ t(c === 'sms' ? 'tabs.sms' : 'tabs.email') }}
        <CircleAlert
          v-if="status[c] === 'invalid'"
          :data-testid="`channel-tab-error-${c}`"
          class="size-4 text-error"
          role="img"
          :aria-label="t('tabs.hasErrors')"
        />
      </button>
    </div>
    <div
      v-for="c in CHANNELS"
      :id="panelId(c)"
      :key="c"
      role="tabpanel"
      :data-testid="`content-panel-${c}`"
      :aria-labelledby="tabId(c)"
      :hidden="c !== modelValue"
      tabindex="0"
      class="outline-none focus-visible:shadow-focus"
    >
      <slot :name="c" />
    </div>
  </div>
</template>
