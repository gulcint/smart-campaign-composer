<script setup lang="ts">
// ui.md §3.4: five steps; access follows the campaign state table (ui.md §1).
import { Check, Lock } from '@lucide/vue'
import { computed } from 'vue'

import type { CampaignState, StepId } from '@/api/types'
import { t } from '@/i18n'
import { isStepAccessible, STEPS, stepStatus } from '@/lib/steps'
import { cn } from '@/lib/utils'

const props = defineProps<{ campaignState: CampaignState | null; current: StepId }>()
const emit = defineEmits<{ navigate: [step: StepId] }>()

const steps = computed(() =>
  STEPS.map((id, i) => ({
    id,
    n: i + 1,
    label: t(`step.${id}`),
    status: stepStatus(props.campaignState, props.current, id),
  })),
)
const currentIndex = computed(() => STEPS.indexOf(props.current))

function go(step: StepId) {
  if (!isStepAccessible(props.campaignState, step)) return
  emit('navigate', step)
}
</script>

<template>
  <nav data-testid="stepper" :aria-label="t('stepper.label')" class="w-full">
    <!-- Compact summary below 768 px (DESIGN.md §8.6); the step row stays tappable and every
         step keeps a 44 px touch target below 1024 px. -->
    <p class="mb-1 text-caption text-ink-600 md:hidden" aria-hidden="true">
      {{
        t('stepper.compact', {
          n: currentIndex + 1,
          total: STEPS.length,
          label: t(`step.${current}`),
        })
      }}
    </p>

    <ol class="flex items-center gap-1 overflow-x-auto md:flex-wrap md:gap-2">
      <li v-for="(s, i) in steps" :key="s.id" class="flex items-center gap-2">
        <button
          type="button"
          :data-testid="`stepper-step-${s.id}`"
          :data-status="s.status"
          :aria-disabled="s.status === 'locked' ? 'true' : undefined"
          :aria-current="s.status === 'active' ? 'step' : undefined"
          :title="s.status === 'locked' ? t('stepper.locked') : undefined"
          :class="
            cn(
              'group flex min-h-11 min-w-11 items-center justify-center gap-2 rounded-pill px-2 py-1 text-body-sm transition-colors lg:min-h-0 lg:min-w-0',
              s.status === 'locked' ? 'cursor-not-allowed text-ink-300' : 'hover:bg-accent',
              s.status === 'active' && 'font-semibold text-accent-text',
              s.status === 'completed' && 'text-ink-700',
              s.status === 'upcoming' && 'text-ink-500',
            )
          "
          @click="go(s.id)"
        >
          <span
            :class="
              cn(
                'flex size-6 items-center justify-center rounded-full text-caption',
                s.status === 'completed' && 'bg-primary text-primary-foreground',
                s.status === 'active' && 'border-2 border-primary text-accent-text',
                s.status === 'upcoming' && 'border border-ink-300 text-ink-500',
                s.status === 'locked' && 'border border-ink-200 text-ink-300',
              )
            "
            aria-hidden="true"
          >
            <Check v-if="s.status === 'completed'" class="size-3.5" />
            <Lock v-else-if="s.status === 'locked'" class="size-3" />
            <template v-else>{{ s.n }}</template>
          </span>
          <span :class="s.status === 'active' ? '' : 'sr-only md:not-sr-only'">{{ s.label }}</span>
        </button>
        <span
          v-if="i < steps.length - 1"
          class="hidden h-px w-6 bg-border-default lg:block"
          aria-hidden="true"
        />
      </li>
    </ol>
  </nav>
</template>
