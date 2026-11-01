<script setup lang="ts">
// ui.md §3.28 (REQ-ABT-04): Variant A / Variant B tabs, the top of the variant → language →
// channel hierarchy on the content and approval steps. Same WAI-ARIA tabs pattern as
// LanguageTabs (roving tabindex, automatic activation, wrap-around, Home/End); the panel belongs
// to the page, so the tabs carry aria-controls only when the page passes an idBase.
import { CircleAlert } from '@lucide/vue'
import { nextTick, ref } from 'vue'

import type { ContentPhase } from '@/api/types'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { ContentVariant } from '@/lib/variant'

const props = defineProps<{
  modelValue: ContentVariant
  /** Summary phase of each variant (invalid if any of its fields is invalid). */
  phases: Record<ContentVariant, ContentPhase>
  errors: Record<ContentVariant, boolean>
  /** Id prefix shared with the page's tabpanel, if it has one (`<idBase>-panel`). */
  idBase?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [variant: ContentVariant] }>()

const VARIANTS: readonly ContentVariant[] = ['a', 'b']
const tabId = (v: ContentVariant) => (props.idBase ? `${props.idBase}-tab-${v}` : undefined)
const tabEls = ref<Partial<Record<ContentVariant, HTMLElement>>>({})

function select(v: ContentVariant, focus = false) {
  if (v !== props.modelValue) emit('update:modelValue', v)
  if (focus) void nextTick(() => tabEls.value[v]?.focus())
}

function onKeydown(e: KeyboardEvent, from: ContentVariant) {
  const i = VARIANTS.indexOf(from)
  const n = VARIANTS.length
  let to: ContentVariant | undefined
  if (e.key === 'ArrowRight') to = VARIANTS[(i + 1) % n]
  else if (e.key === 'ArrowLeft') to = VARIANTS[(i - 1 + n) % n]
  else if (e.key === 'Home') to = VARIANTS[0]
  else if (e.key === 'End') to = VARIANTS[n - 1]
  if (!to) return
  e.preventDefault()
  select(to, true)
}
</script>

<template>
  <div
    data-testid="content-variant-tabs"
    role="tablist"
    :aria-label="t('tabs.variants')"
    class="flex w-fit gap-1 border-b border-border-default"
  >
    <button
      v-for="v in VARIANTS"
      :id="tabId(v)"
      :key="v"
      :ref="(el) => (tabEls[v] = (el as HTMLElement | null) ?? undefined)"
      type="button"
      role="tab"
      :aria-controls="idBase ? `${idBase}-panel` : undefined"
      :data-testid="`content-variant-tab-${v}`"
      :data-status="phases[v]"
      :data-has-error="errors[v] ? 'true' : 'false'"
      :data-state="v === modelValue ? 'active' : 'inactive'"
      :aria-selected="v === modelValue ? 'true' : 'false'"
      :tabindex="v === modelValue ? 0 : -1"
      :class="
        cn(
          'relative inline-flex h-9 items-center gap-2 px-3 text-body-sm font-semibold text-ink-600 max-lg:h-11',
          'transition-colors outline-none focus-visible:shadow-focus',
          'data-[state=active]:text-accent-text',
          'after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:rounded-pill after:bg-accent-text',
          'after:opacity-0 data-[state=active]:after:opacity-100',
        )
      "
      @click="select(v)"
      @keydown="onKeydown($event, v)"
    >
      <!-- Letter mark: a small square monogram, so A/B reads at a glance before the label. Drawn
           by a pseudo-element so the tab's text stays exactly its label. -->
      <span
        aria-hidden="true"
        :data-mark="v.toUpperCase()"
        :class="
          cn(
            'inline-flex size-5 items-center justify-center rounded-sm border font-mono text-caption leading-none',
            'before:content-[attr(data-mark)]',
            v === modelValue
              ? 'border-accent-text text-accent-text'
              : 'border-border-default text-ink-500',
          )
        "
      />
      {{ t(v === 'a' ? 'variant.a' : 'variant.b') }}
      <CircleAlert
        v-if="errors[v]"
        :data-testid="`content-variant-tab-error-${v}`"
        class="size-4 text-error"
        role="img"
        :aria-label="t('tabs.hasErrors')"
      />
    </button>
  </div>
</template>
