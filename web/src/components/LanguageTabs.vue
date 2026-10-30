<script setup lang="ts">
// ui.md §3.27 (REQ-LNG-04): Turkish / English content version tabs, one level above ChannelTabs.
// Same WAI-ARIA tabs pattern as ChannelTabs (roving tabindex, automatic activation, wrap-around,
// Home/End); the panels belong to the page, so the tabs carry no aria-controls.
import { CircleAlert } from '@lucide/vue'
import { nextTick, ref } from 'vue'

import type { ContentPhase } from '@/api/types'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

type Language = 'tr' | 'en'

const props = defineProps<{
  modelValue: Language
  /** Summary phase of each language (invalid if any of its fields is invalid). */
  phases: Record<Language, ContentPhase>
  errors: Record<Language, boolean>
  /** Id prefix shared with the page's tabpanel (`<idBase>-panel`, labelled by `<idBase>-tab-<l>`). */
  idBase: string
}>()
const emit = defineEmits<{ 'update:modelValue': [language: Language] }>()

const LANGUAGES: readonly Language[] = ['tr', 'en']
const tabId = (l: Language) => `${props.idBase}-tab-${l}`
const tabEls = ref<Partial<Record<Language, HTMLElement>>>({})

function select(l: Language, focus = false) {
  if (l !== props.modelValue) emit('update:modelValue', l)
  if (focus) void nextTick(() => tabEls.value[l]?.focus())
}

function onKeydown(e: KeyboardEvent, from: Language) {
  const i = LANGUAGES.indexOf(from)
  const n = LANGUAGES.length
  let to: Language | undefined
  if (e.key === 'ArrowRight') to = LANGUAGES[(i + 1) % n]
  else if (e.key === 'ArrowLeft') to = LANGUAGES[(i - 1 + n) % n]
  else if (e.key === 'Home') to = LANGUAGES[0]
  else if (e.key === 'End') to = LANGUAGES[n - 1]
  if (!to) return
  e.preventDefault()
  select(to, true)
}
</script>

<template>
  <div
    data-testid="language-tabs"
    role="tablist"
    :aria-label="t('tabs.languages')"
    class="flex w-fit gap-1 border-b border-border-default"
  >
    <button
      v-for="l in LANGUAGES"
      :id="tabId(l)"
      :key="l"
      :ref="(el) => (tabEls[l] = (el as HTMLElement | null) ?? undefined)"
      type="button"
      role="tab"
      :aria-controls="`${idBase}-panel`"
      :data-testid="`language-tab-${l}`"
      :data-status="phases[l]"
      :data-has-error="errors[l] ? 'true' : 'false'"
      :data-state="l === modelValue ? 'active' : 'inactive'"
      :aria-selected="l === modelValue ? 'true' : 'false'"
      :tabindex="l === modelValue ? 0 : -1"
      :class="
        cn(
          'relative inline-flex h-9 items-center gap-1.5 px-3 text-body-sm font-semibold text-ink-600 max-lg:h-11',
          'transition-colors outline-none focus-visible:shadow-focus',
          'data-[state=active]:text-accent-text',
          'after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-pill after:bg-accent-text',
          'after:opacity-0 data-[state=active]:after:opacity-100',
        )
      "
      @click="select(l)"
      @keydown="onKeydown($event, l)"
    >
      {{ t(l === 'tr' ? 'language.tr' : 'language.en') }}
      <CircleAlert
        v-if="errors[l]"
        :data-testid="`language-tab-error-${l}`"
        class="size-4 text-error"
        role="img"
        :aria-label="t('tabs.hasErrors')"
      />
    </button>
  </div>
</template>
