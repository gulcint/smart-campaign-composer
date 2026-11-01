<script setup lang="ts">
// TSİ date-time field for plan mode (ui.md §3.18). The native datetime-local input stays the
// value carrier (typing, form semantics, the `YYYY-MM-DDTHH:MM` wall-clock value); its browser
// popup is replaced by a Reka Popover + Calendar + TimeField, because the native popup cannot be
// themed (Chromium paints it blue, ignores `accent-color`, labels it in the browser's language
// and shows "today" in the browser's time zone, not TSİ). Attributes fall through to the input.
import { CalendarDate, Time } from '@internationalized/date'
import { CalendarClock, ChevronLeft, ChevronRight } from '@lucide/vue'
import {
  CalendarCell,
  CalendarCellTrigger,
  CalendarGrid,
  CalendarGridBody,
  CalendarGridHead,
  CalendarGridRow,
  CalendarHeadCell,
  CalendarHeader,
  CalendarHeading,
  CalendarNext,
  CalendarPrev,
  CalendarRoot,
  PopoverAnchor,
  PopoverClose,
  PopoverContent,
  PopoverPortal,
  PopoverRoot,
  PopoverTrigger,
  TimeFieldInput,
  TimeFieldRoot,
  type DateValue,
} from 'reka-ui'
import { computed, nextTick, ref, useId } from 'vue'

import { t } from '@/i18n'
import { cn } from '@/lib/utils'

import Button from './Button.vue'

defineOptions({ inheritAttrs: false })

const props = defineProps<{
  modelValue: string
  /** TSİ 'YYYY-MM-DDTHH:MM' bounds, as on the input. */
  min: string
  max: string
  /** TSİ 'YYYY-MM-DD' of today, marked in the calendar. */
  today: string
  invalid?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const LOCALE = 'tr-TR'
const VALUE_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/
const DEFAULT_TIME = '09:00'
const timeLabelId = `${useId()}-time`

const open = ref(false)
const placeholder = ref<DateValue>()
const fieldRef = ref<HTMLElement>()

const pad = (n: number) => String(n).padStart(2, '0')

function toDate(ymd: string): CalendarDate | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(ymd)
  return m ? new CalendarDate(Number(m[1]), Number(m[2]), Number(m[3])) : undefined
}

const parts = computed(() => {
  const m = VALUE_RE.exec(props.modelValue)
  return m ? { date: props.modelValue.slice(0, 10), time: props.modelValue.slice(11, 16) } : null
})
const minDate = computed(() => toDate(props.min))
const maxDate = computed(() => toDate(props.max))

// A picked day keeps the chosen time; without one it starts at 09:00 pulled into the window.
function timeFor(date: string): string {
  if (parts.value) return parts.value.time
  const minTime = props.min.slice(11, 16)
  const maxTime = props.max.slice(11, 16)
  if (date === props.min.slice(0, 10) && DEFAULT_TIME < minTime) return minTime
  if (date === props.max.slice(0, 10) && DEFAULT_TIME > maxTime) return maxTime
  return DEFAULT_TIME
}

const dateModel = computed<DateValue | undefined>({
  get: () => (parts.value ? toDate(parts.value.date) : undefined),
  set: (v) => {
    if (!v) return
    const date = v.toString()
    emit('update:modelValue', `${date}T${timeFor(date)}`)
  },
})

const timeModel = computed<Time | undefined>({
  get: () => {
    if (!parts.value) return undefined
    const [h, m] = parts.value.time.split(':').map(Number) as [number, number]
    return new Time(h, m)
  },
  set: (v) => {
    if (!v) return
    const date = parts.value?.date ?? props.min.slice(0, 10)
    emit('update:modelValue', `${date}T${pad(v.hour)}:${pad(v.minute)}`)
  },
})

function onOpenChange(v: boolean) {
  if (v) placeholder.value = dateModel.value ?? minDate.value
  open.value = v
}

function onInput(ev: Event) {
  emit('update:modelValue', (ev.target as HTMLInputElement).value)
}

// The field is part of the control: focusing or clicking it keeps the popover open (Alt+↓ opens
// it while the field holds focus), so typing and picking stay in sync.
function onInteractOutside(ev: Event) {
  if (fieldRef.value?.contains(ev.target as Node)) ev.preventDefault()
}

// Opening lands on the selected day (else the first selectable one), the calendar's roving stop.
function onOpenAutoFocus(ev: Event) {
  ev.preventDefault()
  const content = ev.target as HTMLElement
  void nextTick(() => {
    const cells = '[data-reka-calendar-cell-trigger]:not([data-disabled]):not([data-outside-view])'
    const day =
      content.querySelector<HTMLElement>(`${cells}[data-selected]`) ??
      content.querySelector<HTMLElement>(cells)
    day?.focus()
  })
}

// Chromium opens its own popup on Alt+↓ / F4; ours opens instead.
function onKeydown(ev: KeyboardEvent) {
  if ((ev.altKey && ev.key === 'ArrowDown') || ev.key === 'F4') {
    ev.preventDefault()
    onOpenChange(true)
  }
}

const navButton =
  'inline-flex size-10 items-center justify-center rounded-pill text-ink-600 outline-none transition-colors duration-[120ms] hover:bg-surface-sunken hover:text-ink-900 focus-visible:shadow-focus data-[disabled]:cursor-not-allowed data-[disabled]:text-ink-300 data-[disabled]:hover:bg-transparent'
</script>

<template>
  <PopoverRoot :open="open" @update:open="onOpenChange">
    <PopoverAnchor as-child>
      <div ref="fieldRef" class="relative">
        <input
          v-bind="$attrs"
          type="datetime-local"
          step="60"
          :value="modelValue"
          :min="min"
          :max="max"
          :class="
            cn(
              'datetime-field block h-11 w-full rounded-input border bg-surface-card ps-4 pe-12 text-body text-ink-900 outline-none',
              'focus-visible:border-primary focus-visible:shadow-focus',
              invalid ? 'border-[1.5px] border-error' : 'border-border-default',
            )
          "
          @input="onInput"
          @keydown="onKeydown"
        />
        <PopoverTrigger
          data-testid="datetime-picker-trigger"
          :aria-label="t('datetimePicker.open')"
          :class="
            cn(
              'absolute inset-y-0 end-0 inline-flex w-11 items-center justify-center rounded-input outline-none',
              'transition-colors duration-[120ms] hover:text-ink-900 focus-visible:shadow-focus',
              'data-[state=open]:text-accent-text',
              invalid ? 'text-error' : 'text-ink-600',
            )
          "
        >
          <CalendarClock class="size-5" aria-hidden="true" />
        </PopoverTrigger>
      </div>
    </PopoverAnchor>
    <PopoverPortal>
      <PopoverContent
        data-testid="datetime-picker"
        :aria-label="t('datetimePicker.dialog')"
        side="bottom"
        align="end"
        :side-offset="8"
        :collision-padding="16"
        class="z-50 flex w-[min(20rem,calc(100vw-2rem))] flex-col gap-3 rounded-card border border-border-subtle bg-surface-raised p-4 text-ink-900 shadow-raised data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0 data-[state=open]:zoom-in-95 motion-reduce:animate-none"
        @open-auto-focus="onOpenAutoFocus"
        @interact-outside="onInteractOutside"
      >
        <CalendarRoot
          v-slot="{ grid, weekDays }"
          v-model="dateModel"
          v-model:placeholder="placeholder"
          data-testid="datetime-picker-calendar"
          :locale="LOCALE"
          :week-starts-on="1"
          weekday-format="short"
          :min-value="minDate"
          :max-value="maxDate"
          :calendar-label="t('datetimePicker.calendar')"
          fixed-weeks
          prevent-deselect
          class="flex flex-col gap-2"
        >
          <CalendarHeader class="flex items-center justify-between gap-2">
            <CalendarPrev :class="navButton" :aria-label="t('datetimePicker.prevMonth')">
              <ChevronLeft class="size-5" aria-hidden="true" />
            </CalendarPrev>
            <CalendarHeading class="text-h4 text-accent-heading capitalize" />
            <CalendarNext :class="navButton" :aria-label="t('datetimePicker.nextMonth')">
              <ChevronRight class="size-5" aria-hidden="true" />
            </CalendarNext>
          </CalendarHeader>
          <CalendarGrid
            v-for="month in grid"
            :key="month.value.toString()"
            class="w-full border-collapse"
          >
            <CalendarGridHead>
              <CalendarGridRow class="grid grid-cols-7">
                <CalendarHeadCell
                  v-for="day in weekDays"
                  :key="day"
                  class="py-1 text-caption text-ink-500"
                >
                  {{ day }}
                </CalendarHeadCell>
              </CalendarGridRow>
            </CalendarGridHead>
            <CalendarGridBody>
              <CalendarGridRow
                v-for="(week, i) in month.rows"
                :key="i"
                class="grid grid-cols-7 justify-items-center"
              >
                <CalendarCell v-for="d in week" :key="d.toString()" :date="d" class="p-0.5">
                  <CalendarCellTrigger
                    :day="d"
                    :month="month.value"
                    :data-tsi-today="d.toString() === today ? '' : undefined"
                    :class="
                      cn(
                        'relative inline-flex size-9 items-center justify-center rounded-pill text-body-sm text-ink-900 tabular-nums outline-none',
                        'transition-colors duration-[120ms] hover:bg-surface-sunken focus-visible:shadow-focus',
                        'data-[tsi-today]:font-semibold data-[tsi-today]:text-accent-numeral',
                        'data-[outside-view]:text-ink-300',
                        'data-[disabled]:cursor-not-allowed data-[disabled]:text-ink-300 data-[disabled]:hover:bg-transparent',
                        'data-[selected]:bg-primary data-[selected]:font-semibold data-[selected]:text-primary-foreground data-[selected]:hover:bg-primary',
                      )
                    "
                  />
                </CalendarCell>
              </CalendarGridRow>
            </CalendarGridBody>
          </CalendarGrid>
        </CalendarRoot>
        <div class="flex items-center justify-between gap-3 border-t border-border-subtle pt-3">
          <span :id="timeLabelId" class="text-body-sm font-medium text-ink-700">
            {{ t('datetimePicker.time') }}
          </span>
          <div class="flex items-center gap-2">
            <!-- Time segments preventDefault every key, Escape included, so the popover layer
                 would not dismiss on its own; close it here. -->
            <TimeFieldRoot
              v-slot="{ segments }"
              v-model="timeModel"
              data-testid="datetime-picker-time"
              :aria-labelledby="timeLabelId"
              :locale="LOCALE"
              :hour-cycle="24"
              granularity="minute"
              hide-time-zone
              class="flex h-11 items-center rounded-input border border-border-default bg-surface-card px-3 text-body text-ink-900 tabular-nums focus-within:border-primary"
              @keydown.esc="onOpenChange(false)"
            >
              <template v-for="item in segments" :key="item.part">
                <TimeFieldInput
                  v-if="item.part === 'literal'"
                  :part="item.part"
                  class="text-ink-500"
                >
                  {{ item.value }}
                </TimeFieldInput>
                <TimeFieldInput
                  v-else
                  :part="item.part"
                  class="rounded-[4px] px-0.5 outline-none focus:bg-brand-status-bg data-[placeholder]:text-ink-500"
                >
                  {{ item.value }}
                </TimeFieldInput>
              </template>
            </TimeFieldRoot>
            <span
              class="rounded-sm bg-surface-sunken px-2 py-0.5 font-mono text-caption text-ink-600"
              >{{ t('schedule.tz') }}</span
            >
          </div>
        </div>
        <PopoverClose as-child>
          <Button data-testid="datetime-picker-done" variant="secondary" size="sm" class="self-end">
            {{ t('datetimePicker.done') }}
          </Button>
        </PopoverClose>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
