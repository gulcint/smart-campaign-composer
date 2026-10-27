<script setup lang="ts">
// Page header for a campaign step (DESIGN.md §8.3a): where the user is ("Adım 3/5 · role"),
// the one h1 of the page (`page-title`, ui.md §1), a one-line purpose, the step's status
// badge and the next action. Content and Approval use it so the two steps read differently.
import { ArrowRight } from '@lucide/vue'
import { computed } from 'vue'

import type { StepId } from '@/api/types'
import { t } from '@/i18n'
import { STEPS } from '@/lib/steps'

import StatusBadge from './StatusBadge.vue'

const props = defineProps<{
  step: StepId
  role: string
  title: string
  subtitle: string
  statusKind: 'content' | 'campaign'
  status: string | null
  next: string | null
}>()

const n = computed(() => STEPS.indexOf(props.step) + 1)
</script>

<template>
  <header data-testid="step-header" :data-step="step" class="flex flex-col gap-4 pt-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="flex min-w-0 flex-col gap-1">
        <p class="text-overline text-accent-text" lang="tr">
          <!-- The compact stepper already says "Adım n/5" below 768 px (DESIGN.md §8.6). -->
          <span class="hidden md:inline"
            >{{ t('stepHeader.step', { n, total: STEPS.length }) }} · </span
          >{{ role }}
        </p>
        <h1 data-testid="page-title" tabindex="-1" class="text-h1 text-ink-900 outline-none">
          {{ title }}
        </h1>
        <p class="max-w-2xl text-body text-ink-600">{{ subtitle }}</p>
      </div>
      <StatusBadge
        v-if="status"
        data-testid="step-header-status"
        :kind="statusKind"
        :status="status"
        class="mt-1"
      />
    </div>
    <div class="flex flex-wrap items-center gap-3">
      <p
        v-if="next"
        data-testid="step-header-next"
        class="inline-flex items-center gap-2 rounded-pill border border-border-default bg-surface-card px-3 py-1.5 text-body-sm"
      >
        <ArrowRight class="size-4 text-brand-icon" aria-hidden="true" />
        <span class="text-ink-500">{{ t('stepHeader.next') }}</span>
        <span class="font-semibold text-ink-900">{{ next }}</span>
      </p>
      <slot />
    </div>
  </header>
</template>
