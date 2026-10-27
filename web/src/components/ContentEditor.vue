<script setup lang="ts">
// ui.md §3.14 (REQ-APR-01, -02, -03): edits the full Content (both channels); `channel` only
// picks the visible fields. `dirty` belongs to the parent. On a 409 content_edit_conflict
// (D40) the conflict panel offers reload / keep-mine and saving is blocked until resolved.
import { Lock, RefreshCw, Save, TriangleAlert } from '@lucide/vue'
import { computed, nextTick, ref, useId, watch } from 'vue'

import type { ChannelKey, Content, FieldError, FieldKey, WorstCase } from '@/api/types'
import { routeFieldErrors } from '@/composables/useContentGeneration'
import type { MessageId } from '@/i18n'
import { t } from '@/i18n'
import { diffEdit, fitInsertion, runeCount } from '@/lib/text-cap'
import { cn } from '@/lib/utils'
import { ALLOWED_VARIABLES } from '@/lib/variables'

import Button from './Button.vue'
import CharCounter from './CharCounter.vue'
import InlineError from './InlineError.vue'
import VariableChip from './VariableChip.vue'

export interface EditConflict {
  currentVersionId: string | null
  currentVersionNumber: number | null
}

const props = withDefaults(
  defineProps<{
    modelValue: Content
    channel: ChannelKey
    mode: 'readonly' | 'editable' | 'locked'
    dirty: boolean
    saving?: boolean
    fieldErrors?: FieldError[]
    worstCase?: WorstCase | null
    conflict?: EditConflict | null
  }>(),
  { saving: false, fieldErrors: () => [], worstCase: null, conflict: null },
)
const emit = defineEmits<{
  'update:modelValue': [content: Content]
  save: [content: Content]
  'reload-latest': []
  'keep-mine': []
}>()

interface FieldDef {
  key: FieldKey
  path: string
  label: MessageId
  multiline: boolean
  limit: 160 | 80 | null
  /** CharCounter field (SMS body and email subject only). */
  counter: 'sms-body' | 'email-subject' | null
}

const FIELDS: Record<ChannelKey, FieldDef[]> = {
  sms: [
    {
      key: 'sms-body',
      path: 'sms.body',
      label: 'editor.smsBody',
      multiline: true,
      limit: 160,
      counter: 'sms-body',
    },
  ],
  email: [
    {
      key: 'email-subject',
      path: 'email.subject',
      label: 'editor.emailSubject',
      multiline: false,
      limit: 80,
      counter: 'email-subject',
    },
    {
      key: 'email-body',
      path: 'email.body',
      label: 'editor.emailBody',
      multiline: true,
      limit: null,
      counter: null,
    },
  ],
}
const BODY_OF: Record<ChannelKey, FieldKey> = { sms: 'sms-body', email: 'email-body' }

const uid = useId()
const inputId = (f: FieldKey) => `${uid}-${f}`
const warningId = `${uid}-conflict-warning`

const visible = computed(() => FIELDS[props.channel])
const readonly = computed(() => props.mode !== 'editable' || props.saving)
const chipsDisabled = computed(() => props.mode !== 'editable' || props.saving)
const canSave = computed(
  () => props.mode === 'editable' && props.dirty && !props.saving && props.conflict === null,
)
const errorsOf = computed(() => routeFieldErrors(props.fieldErrors))

function valueOf(c: Content, f: FieldKey): string {
  if (f === 'sms-body') return c.sms.body
  return f === 'email-subject' ? c.email.subject : c.email.body
}

function withValue(c: Content, f: FieldKey, v: string): Content {
  if (f === 'sms-body') return { ...c, sms: { ...c.sms, body: v } }
  if (f === 'email-subject') return { ...c, email: { ...c.email, subject: v } }
  return { ...c, email: { ...c.email, body: v } }
}

function overLimit(d: FieldDef): boolean {
  return d.limit !== null && runeCount(valueOf(props.modelValue, d.key)) > d.limit
}

function invalid(d: FieldDef): boolean {
  return errorsOf.value[d.key].length > 0 || overLimit(d)
}

/** One id per field (ui.md §3.14 e): the field's own error wins over path "" errors. */
function primaryCode(d: FieldDef): string | undefined {
  const errs = errorsOf.value[d.key]
  return (errs.find((e) => e.path === d.path) ?? errs[0])?.code
}

function describedBy(d: FieldDef): string | undefined {
  const ids: string[] = []
  if (d.limit !== null) ids.push(`char-counter-${d.key}`)
  if (errorsOf.value[d.key].length > 0) ids.push(`inline-error-${d.key}`)
  return ids.length ? ids.join(' ') : undefined
}

function worstOf(d: FieldDef): number | null {
  if (!props.worstCase) return null
  return d.key === 'sms-body' ? props.worstCase.sms_runes : props.worstCase.subject_runes
}

const els: Partial<Record<FieldKey, HTMLInputElement | HTMLTextAreaElement>> = {}
let lastFocused: FieldKey | null = null

function setEl(f: FieldKey, el: unknown) {
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) els[f] = el
  else delete els[f]
}

type Field = HTMLInputElement | HTMLTextAreaElement

/** Writes a capped value into the field itself (its native input was prevented or undone). */
function applyValue(d: FieldDef, el: Field, v: string, caret: number) {
  el.value = v
  el.setSelectionRange(caret, caret)
  if (v !== valueOf(props.modelValue, d.key))
    emit('update:modelValue', withValue(props.modelValue, d.key, v))
}

/**
 * Input cap (ui.md §3.14 d2, D48): replaces the selection with the fitting prefix of `data` and
 * prevents the native input when `data` does not fit whole. Uncapped fields are left alone.
 */
function capInsert(d: FieldDef, e: Event, data: string) {
  if (d.limit === null || readonly.value) return
  const el = e.target as Field
  const v = el.value
  const start = el.selectionStart ?? v.length
  const end = el.selectionEnd ?? start
  const text = d.multiline ? data.replace(/\r\n?/g, '\n') : data.replace(/[\r\n]/g, '')
  const fit = fitInsertion(v.slice(0, start), text, v.slice(end), d.limit, runeCount(v))
  if (fit === text) return
  e.preventDefault()
  if (fit !== '') applyValue(d, el, v.slice(0, start) + fit + v.slice(end), start + fit.length)
}

function onPaste(d: FieldDef, e: ClipboardEvent) {
  capInsert(d, e, e.clipboardData?.getData('text/plain') ?? '')
}

function onBeforeInput(d: FieldDef, e: InputEvent) {
  // Drop targets and IME compositions have no reliable selection here: onInput caps them.
  if (e.isComposing || e.inputType === 'insertFromDrop') return
  if (!e.inputType.startsWith('insert')) return
  const lineBreak = e.inputType === 'insertLineBreak' || e.inputType === 'insertParagraph'
  capInsert(d, e, e.data ?? e.dataTransfer?.getData('text/plain') ?? (lineBreak ? '\n' : ''))
}

/** Emits the field value; a capped field that grew past the cap keeps only the fitting part. */
function onInput(d: FieldDef, e: Event) {
  if ((e as InputEvent).isComposing) return
  const el = e.target as Field
  const v = el.value
  const prev = valueOf(props.modelValue, d.key)
  const n = runeCount(v)
  if (d.limit === null || readonly.value || n <= d.limit || n <= runeCount(prev)) {
    emit('update:modelValue', withValue(props.modelValue, d.key, v))
    return
  }
  const { start, prevEnd, nextEnd } = diffEdit(prev, v)
  const before = prev.slice(0, start)
  const after = prev.slice(prevEnd)
  const fit = fitInsertion(before, v.slice(start, nextEnd), after, d.limit, runeCount(prev))
  applyValue(d, el, before + fit + after, start + fit.length)
}

function save() {
  if (canSave.value) emit('save', props.modelValue)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    save()
  }
}

/** Inserts `{{name}}` at the selection of the last focused visible field (else the body). */
function insertVariable(name: string) {
  if (chipsDisabled.value) return
  const visibleKeys = visible.value.map((d) => d.key)
  const focused = lastFocused !== null && visibleKeys.includes(lastFocused)
  const target = focused ? (lastFocused as FieldKey) : BODY_OF[props.channel]
  const el = els[target]
  const current = valueOf(props.modelValue, target)
  const token = `{{${name}}}`
  const start = focused && el ? (el.selectionStart ?? current.length) : current.length
  const end = focused && el ? (el.selectionEnd ?? start) : start
  // ui.md §3.14 d2: a chip that does not fit whole is not inserted.
  const limit = visible.value.find((d) => d.key === target)?.limit ?? null
  const fits =
    limit === null ||
    fitInsertion(current.slice(0, start), token, current.slice(end), limit, runeCount(current)) ===
      token
  if (!fits) return
  const next = current.slice(0, start) + token + current.slice(end)
  emit('update:modelValue', withValue(props.modelValue, target, next))
  lastFocused = target
  void nextTick(() => {
    const field = els[target]
    if (!field) return
    const caret = start + token.length
    field.focus()
    field.setSelectionRange(caret, caret)
  })
}

const reloadBtn = ref<{ $el: HTMLElement } | null>(null)
watch(
  () => props.conflict !== null,
  (open) => {
    if (open) void nextTick(() => reloadBtn.value?.$el.focus())
  },
  { flush: 'post' },
)
</script>

<template>
  <div class="flex flex-col gap-4">
    <div
      v-if="conflict"
      data-testid="editor-conflict"
      role="alert"
      :data-current-version-id="conflict.currentVersionId ?? ''"
      :data-value="conflict.currentVersionNumber ?? ''"
      class="flex flex-col gap-3 rounded-card border border-warning/40 bg-warning-bg p-4"
    >
      <p class="flex items-start gap-2 text-body font-semibold text-ink-900">
        <TriangleAlert class="mt-0.5 size-5 shrink-0 text-warning" aria-hidden="true" />
        {{ t('editor.conflict.title', { n: conflict.currentVersionNumber ?? '?' }) }}
      </p>
      <div class="flex flex-wrap gap-2">
        <Button
          ref="reloadBtn"
          data-testid="editor-conflict-reload"
          size="sm"
          @click="emit('reload-latest')"
        >
          <RefreshCw aria-hidden="true" />
          {{ t('editor.conflict.reload') }}
        </Button>
        <Button
          data-testid="editor-conflict-keep-mine"
          variant="secondary"
          size="sm"
          :aria-describedby="warningId"
          @click="emit('keep-mine')"
        >
          {{ t('editor.conflict.keepMine') }}
        </Button>
      </div>
      <p :id="warningId" data-testid="editor-conflict-warning" class="text-body-sm text-ink-700">
        {{ t('editor.conflict.keepMineWarning') }}
      </p>
    </div>

    <p
      v-if="mode === 'locked'"
      data-testid="editor-locked-note"
      class="flex items-center gap-2 rounded-sm bg-surface-sunken px-3 py-2 text-body-sm text-ink-600"
    >
      <Lock class="size-4" aria-hidden="true" />
      {{ t('editor.lockedNote') }}
    </p>

    <div v-for="d in visible" :key="d.key" class="flex flex-col gap-1.5">
      <label :for="inputId(d.key)" class="text-h4 text-accent-heading">{{ t(d.label) }}</label>
      <textarea
        v-if="d.multiline"
        :id="inputId(d.key)"
        :ref="(el) => setEl(d.key, el)"
        :data-testid="`editor-${d.key}`"
        :value="valueOf(modelValue, d.key)"
        :readonly="readonly"
        :aria-invalid="invalid(d) ? 'true' : undefined"
        :aria-describedby="describedBy(d)"
        :rows="d.key === 'sms-body' ? 4 : 10"
        :class="
          cn(
            'block w-full resize-y rounded-input border bg-surface-card px-4 py-3 text-ink-900 outline-none',
            'focus-visible:border-primary focus-visible:shadow-focus read-only:bg-surface-sunken read-only:text-ink-600',
            d.key === 'sms-body' ? 'text-body-lg' : 'text-body',
            invalid(d) ? 'border-[1.5px] border-error' : 'border-border-default',
          )
        "
        @paste="onPaste(d, $event)"
        @beforeinput="onBeforeInput(d, $event as InputEvent)"
        @input="onInput(d, $event)"
        @compositionend="onInput(d, $event)"
        @focus="lastFocused = d.key"
        @keydown="onKeydown"
      />
      <input
        v-else
        :id="inputId(d.key)"
        :ref="(el) => setEl(d.key, el)"
        type="text"
        :data-testid="`editor-${d.key}`"
        :value="valueOf(modelValue, d.key)"
        :readonly="readonly"
        :aria-invalid="invalid(d) ? 'true' : undefined"
        :aria-describedby="describedBy(d)"
        :class="
          cn(
            'block h-11 w-full rounded-input border bg-surface-card px-4 text-body text-ink-900 outline-none',
            'focus-visible:border-primary focus-visible:shadow-focus read-only:bg-surface-sunken read-only:text-ink-600',
            invalid(d) ? 'border-[1.5px] border-error' : 'border-border-default',
          )
        "
        @paste="onPaste(d, $event)"
        @beforeinput="onBeforeInput(d, $event as InputEvent)"
        @input="onInput(d, $event)"
        @compositionend="onInput(d, $event)"
        @focus="lastFocused = d.key"
        @keydown="onKeydown"
      />
      <div class="flex items-start justify-between gap-3">
        <div
          v-if="errorsOf[d.key].length"
          :id="`inline-error-${d.key}`"
          :data-testid="`inline-error-${d.key}`"
          :data-code="primaryCode(d)"
          class="flex flex-col gap-1"
        >
          <InlineError
            v-for="(e, i) in errorsOf[d.key]"
            :key="`${e.code}-${i}`"
            :path="d.path"
            :code="e.code"
            :limit="e.limit ?? null"
            :actual="e.actual ?? null"
            :with-id="false"
          />
        </div>
        <CharCounter
          v-if="d.counter && d.limit !== null"
          :field="d.counter"
          :text="valueOf(modelValue, d.key)"
          :limit="d.limit"
          :worst-case="worstOf(d)"
          class="ml-auto"
        />
      </div>
    </div>

    <div class="flex flex-wrap items-center justify-between gap-3">
      <div role="group" :aria-label="t('editor.variables')" class="flex flex-wrap gap-2">
        <VariableChip
          v-for="v in ALLOWED_VARIABLES"
          :key="v"
          :name="v"
          variant="insert"
          :disabled="chipsDisabled"
          @insert="insertVariable"
        />
      </div>
      <Button
        data-testid="editor-save"
        :disabled="!canSave"
        :loading="saving"
        :title="t('editor.shortcut')"
        @click="save"
      >
        <Save aria-hidden="true" />
        {{ t('editor.save') }}
      </Button>
    </div>
  </div>
</template>
