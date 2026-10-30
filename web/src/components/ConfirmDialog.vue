<script setup lang="ts">
// ui.md §3.18: confirmation for approve / send / regenerate. Reka AlertDialog gives the
// focus trap, Esc handling, focus return and aria-labelledby; rendered in place (no portal)
// so the dialog stays inside the owning page's DOM. While submitting, confirm is inert and
// cancel / Esc / backdrop do not close it.
// Plan mode (§3.18, REQ-SCH-04): for kind="send" with `allowSchedule` the dialog offers "now"
// or "later"; "later" takes a TSİ wall-clock time, checked against the API's own window
// (the API stays the authority, this only saves a round trip) and emitted as `schedule(utc)`.
import { CalendarClock, Send } from '@lucide/vue'
import {
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogRoot,
  AlertDialogTitle,
  RadioGroupItem,
  RadioGroupRoot,
} from 'reka-ui'
import { computed, ref, useId, watch } from 'vue'

import type { ApiError } from '@/api/errors'
import { errorPresentation } from '@/api/errors'
import { formatNumber, t } from '@/i18n'
import { toTsiInput, toUtcIso } from '@/lib/schedule'
import { cn } from '@/lib/utils'

import Button from './Button.vue'
import InlineError from './InlineError.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    kind: 'approve' | 'send' | 'regenerate'
    counts?: { sms: number; email: number } | null
    submitting?: boolean
    error?: ApiError | null
    allowSchedule?: boolean
    scheduleMinLeadSeconds?: number
    scheduleMaxAheadSeconds?: number
  }>(),
  {
    counts: null,
    submitting: false,
    error: null,
    allowSchedule: false,
    scheduleMinLeadSeconds: 60,
    scheduleMaxAheadSeconds: 2592000,
  },
)
const emit = defineEmits<{
  confirm: []
  cancel: []
  schedule: [scheduledAt: string]
  'update:open': [value: boolean]
}>()

const MINUTE_MS = 60_000
const uid = useId()
const fieldId = `${uid}-scheduled-at`
const hintId = `${uid}-hint`
const noteId = `${uid}-note`
const errorId = `${uid}-error`

type Mode = 'now' | 'later'
const mode = ref<Mode>('now')
const localValue = ref('')
// "Now" for the checks: refreshed on every input, mode change and confirm click.
const nowMs = ref(Date.now())

const scheduling = computed(() => props.kind === 'send' && props.allowSchedule)
const later = computed(() => scheduling.value && mode.value === 'later')

// Every opening starts in "now" mode with an empty time (§3.18).
watch(
  () => props.open,
  (open) => {
    if (!open) return
    mode.value = 'now'
    localValue.value = ''
    nowMs.value = Date.now()
  },
  { immediate: true },
)

watch(mode, () => {
  nowMs.value = Date.now()
})

const minInput = computed(() =>
  toTsiInput(
    new Date(
      Math.ceil((nowMs.value + props.scheduleMinLeadSeconds * 1000) / MINUTE_MS) * MINUTE_MS,
    ).toISOString(),
  ),
)
const maxInput = computed(() =>
  toTsiInput(
    new Date(
      Math.floor((nowMs.value + props.scheduleMaxAheadSeconds * 1000) / MINUTE_MS) * MINUTE_MS,
    ).toISOString(),
  ),
)

const scheduledUtc = computed(() => toUtcIso(localValue.value))
const scheduleErrorCode = computed<'schedule_too_soon' | 'schedule_too_far' | null>(() => {
  if (!later.value || scheduledUtc.value === null) return null
  const at = Date.parse(scheduledUtc.value)
  if (at < nowMs.value + props.scheduleMinLeadSeconds * 1000) return 'schedule_too_soon'
  if (at > nowMs.value + props.scheduleMaxAheadSeconds * 1000) return 'schedule_too_far'
  return null
})
const confirmBlocked = computed(
  () =>
    props.submitting || (later.value && (scheduledUtc.value === null || !!scheduleErrorCode.value)),
)

// Arrow keys pick the neighbour (Reka moves the focus; selection on arrow is made explicit and
// idempotent here, so it does not depend on its focus-to-select heuristic).
function onModeKeydown(ev: KeyboardEvent) {
  if (ev.key === 'ArrowLeft' || ev.key === 'ArrowUp') mode.value = 'now'
  else if (ev.key === 'ArrowRight' || ev.key === 'ArrowDown') mode.value = 'later'
}

function onTimeInput(ev: Event) {
  localValue.value = (ev.target as HTMLInputElement).value
  nowMs.value = Date.now()
}

function onOpenChange(v: boolean) {
  if (!v) cancel()
}

function onConfirm() {
  if (props.submitting) return
  if (!later.value) {
    emit('confirm')
    return
  }
  nowMs.value = Date.now()
  const utc = scheduledUtc.value
  if (utc === null || scheduleErrorCode.value) return
  emit('schedule', utc)
}

function cancel() {
  if (props.submitting) return
  emit('cancel')
  emit('update:open', false)
}
</script>

<template>
  <AlertDialogRoot :open="open" @update:open="onOpenChange">
    <AlertDialogOverlay class="fixed inset-0 z-40 bg-ink-900/30" />
    <AlertDialogContent
      data-testid="confirm-dialog"
      :data-kind="kind"
      :data-mode="kind === 'send' ? (later ? 'later' : 'now') : undefined"
      aria-modal="true"
      class="fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-md overflow-y-auto -translate-x-1/2 -translate-y-1/2 flex-col gap-4 rounded-card-lg bg-surface-raised p-6 shadow-raised"
    >
      <AlertDialogTitle data-testid="confirm-dialog-title" class="text-h3 text-accent-heading">
        {{ later ? t('confirm.schedule.title') : t(`confirm.${kind}.title`) }}
      </AlertDialogTitle>
      <AlertDialogDescription class="text-body text-ink-700">
        {{ later ? t('confirm.schedule.body') : t(`confirm.${kind}.body`) }}
      </AlertDialogDescription>
      <p v-if="kind === 'send' && counts" class="text-body text-ink-700">
        {{ t('audience.channelSms') }}
        <span
          data-testid="confirm-dialog-count-sms"
          :data-value="counts.sms"
          class="font-semibold text-accent-numeral tabular-nums"
          >{{ formatNumber(counts.sms) }}</span
        >
        · {{ t('audience.channelEmail') }}
        <span
          data-testid="confirm-dialog-count-email"
          :data-value="counts.email"
          class="font-semibold text-accent-numeral tabular-nums"
          >{{ formatNumber(counts.email) }}</span
        >
      </p>
      <RadioGroupRoot
        v-if="scheduling"
        v-model="mode"
        data-testid="confirm-dialog-send-mode"
        :aria-label="t('confirm.sendMode.label')"
        orientation="horizontal"
        class="grid grid-cols-1 gap-2 sm:grid-cols-2"
        @keydown="onModeKeydown"
      >
        <RadioGroupItem
          v-for="m in ['now', 'later'] as const"
          :key="m"
          :value="m"
          :data-testid="`confirm-dialog-send-${m}`"
          :class="
            cn(
              'flex min-h-11 items-center justify-center gap-2 rounded-input border px-3 py-2 text-body-sm font-medium text-ink-900 outline-none',
              'focus-visible:shadow-focus data-[state=checked]:border-primary data-[state=checked]:bg-surface-sunken',
              'data-[state=unchecked]:border-border-default data-[state=unchecked]:bg-surface-card',
            )
          "
        >
          <component :is="m === 'now' ? Send : CalendarClock" class="size-4" aria-hidden="true" />
          {{ t(`confirm.sendMode.${m}`) }}
        </RadioGroupItem>
      </RadioGroupRoot>
      <div v-if="later" class="flex flex-col gap-2">
        <div class="flex flex-wrap items-baseline justify-between gap-2">
          <label :for="fieldId" class="text-body-sm font-medium text-ink-900">
            {{ t('confirm.scheduledAt.label') }}
          </label>
          <span data-testid="confirm-dialog-tz" class="font-mono text-caption text-ink-600">{{
            t('schedule.tz')
          }}</span>
        </div>
        <input
          :id="fieldId"
          data-testid="confirm-dialog-scheduled-at"
          type="datetime-local"
          step="60"
          :value="localValue"
          :min="minInput"
          :max="maxInput"
          :data-value="scheduledUtc ?? undefined"
          :aria-invalid="scheduleErrorCode ? 'true' : undefined"
          :aria-describedby="
            [hintId, noteId, scheduleErrorCode ? errorId : null].filter(Boolean).join(' ')
          "
          :class="
            cn(
              'block h-11 w-full rounded-input border bg-surface-card px-4 text-body text-ink-900 outline-none',
              'focus-visible:border-primary focus-visible:shadow-focus',
              scheduleErrorCode ? 'border-[1.5px] border-error' : 'border-border-default',
            )
          "
          @input="onTimeInput"
        />
        <p :id="hintId" class="text-caption text-ink-600">
          {{ t('confirm.scheduledAt.hint', { min: Math.ceil(scheduleMinLeadSeconds / 60) }) }}
        </p>
        <p
          v-if="scheduleErrorCode"
          :id="errorId"
          data-testid="confirm-dialog-schedule-error"
          :data-code="scheduleErrorCode"
          role="alert"
          class="text-body-sm text-error"
        >
          {{ t(`field_error.${scheduleErrorCode}`) }}
        </p>
        <p
          :id="noteId"
          data-testid="confirm-dialog-schedule-note"
          class="text-caption text-ink-600"
        >
          {{ t('confirm.schedule.audienceNote') }}
        </p>
      </div>
      <div
        v-if="error"
        data-testid="confirm-dialog-error"
        :data-code="error.code"
        role="alert"
        class="flex flex-col gap-1 rounded-sm bg-error-bg p-3 text-body-sm text-error"
      >
        <p>{{ t(errorPresentation(error, 'approval').messageId) }}</p>
        <div v-for="(f, i) in error.fields" :key="i" data-testid="field-error" :data-code="f.code">
          <InlineError
            :path="f.path"
            :code="f.code"
            :limit="f.limit ?? null"
            :actual="f.actual ?? null"
          />
        </div>
      </div>
      <div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button data-testid="confirm-dialog-cancel" variant="ghost" @click="cancel">
          {{ t('confirm.cancel') }}
        </Button>
        <Button
          data-testid="confirm-dialog-confirm"
          :disabled="confirmBlocked"
          :loading="submitting"
          @click="onConfirm"
        >
          <CalendarClock v-if="later" aria-hidden="true" />
          <Send v-else-if="kind === 'send'" aria-hidden="true" />
          {{
            later
              ? t('confirm.schedule.confirm')
              : kind === 'send'
                ? t('confirm.send.confirm')
                : t('confirm.confirm')
          }}
        </Button>
      </div>
    </AlertDialogContent>
  </AlertDialogRoot>
</template>
