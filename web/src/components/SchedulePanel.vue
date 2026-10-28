<script setup lang="ts">
// ui.md §3.25 (REQ-SCH-04): the planned send on the tracking page. Shows the TSİ time, a
// per-second countdown, "cancel plan" and "send now". The countdown is not a live region
// (a per-second announcement would drown a screen reader); only the moment it reaches zero is
// announced once through the visually hidden status.
import { CalendarClock, LoaderCircle } from '@lucide/vue'
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'

import { t } from '@/i18n'
import { formatCountdown, formatTsi, remainingSeconds } from '@/lib/schedule'

import Button from './Button.vue'

const props = withDefaults(
  defineProps<{ scheduledAt: string; cancelling?: boolean; sendingNow?: boolean }>(),
  { cancelling: false, sendingNow: false },
)
const emit = defineEmits<{ cancel: []; 'send-now': [] }>()

const titleId = useId()
const now = ref(Date.now())
const timer = setInterval(() => {
  now.value = Date.now()
}, 1000)
onBeforeUnmount(() => clearInterval(timer))

const remaining = computed(() => remainingSeconds(props.scheduledAt, now.value))
const state = computed(() => (remaining.value > 0 ? 'waiting' : 'starting'))
const countdownText = computed(() => {
  if (remaining.value === 0) return t('schedule.starting')
  const { days, hms } = formatCountdown(remaining.value)
  return remaining.value >= 86400
    ? t('schedule.countdownDays', { d: days, hms })
    : t('schedule.countdown', { hms })
})

const liveStatus = ref('')
watch(
  state,
  (s) => {
    if (s === 'starting') liveStatus.value = t('schedule.starting')
  },
  { immediate: true },
)

const busy = computed(() => props.cancelling || props.sendingNow)
</script>

<template>
  <section
    data-testid="schedule-panel"
    :data-state="state"
    :aria-labelledby="titleId"
    class="overflow-hidden rounded-card-lg border border-border-default bg-surface-card shadow-raised"
  >
    <div class="h-1 bg-gradient-brand" aria-hidden="true" />
    <div class="flex flex-col gap-5 p-4 md:p-6">
      <h2 :id="titleId" class="flex items-center gap-2 text-h3 text-accent-heading">
        <CalendarClock class="size-5 shrink-0 text-info" aria-hidden="true" />
        {{ t('schedule.title') }}
      </h2>

      <dl class="grid gap-4 sm:grid-cols-2">
        <div class="flex min-w-0 flex-col gap-1">
          <dt class="text-caption text-ink-500">{{ t('schedule.timeLabel') }}</dt>
          <dd
            data-testid="schedule-time"
            :data-value="scheduledAt"
            class="text-numeral-md text-accent-numeral tabular-nums"
          >
            {{ formatTsi(scheduledAt) }}
          </dd>
        </div>
        <div class="flex min-w-0 flex-col gap-1">
          <dt class="text-caption text-ink-500">{{ t('schedule.remainingLabel') }}</dt>
          <dd
            data-testid="schedule-countdown"
            :data-value="remaining"
            class="flex items-center gap-2 text-numeral-md text-accent-numeral tabular-nums"
          >
            <LoaderCircle
              v-if="state === 'starting'"
              class="size-5 animate-spin motion-reduce:animate-none"
              aria-hidden="true"
            />
            {{ countdownText }}
          </dd>
        </div>
      </dl>

      <span data-testid="schedule-live-status" role="status" class="sr-only">{{ liveStatus }}</span>

      <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end sm:gap-3">
        <Button
          data-testid="schedule-cancel"
          variant="secondary"
          size="lg"
          :disabled="busy"
          :loading="cancelling"
          @click="emit('cancel')"
        >
          {{ t('schedule.cancel') }}
        </Button>
        <Button
          data-testid="schedule-send-now"
          size="lg"
          :disabled="busy"
          :loading="sendingNow"
          @click="emit('send-now')"
        >
          {{ t('schedule.sendNow') }}
        </Button>
      </div>
    </div>
  </section>
</template>
