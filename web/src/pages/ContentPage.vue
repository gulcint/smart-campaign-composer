<script setup lang="ts">
// Step 3 (ui.md §1.1, §3.23; DECISIONS D40, D45; DESIGN.md §8.3): content generation stream,
// editing with optimistic concurrency (X-Expected-Version) and the preview.
import {
  ArrowUpRight,
  CircleCheck,
  FlaskConical,
  Info,
  PencilLine,
  RotateCcw,
  Sparkles,
} from '@lucide/vue'
import { computed, nextTick, onMounted, ref, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { toApiError } from '@/api/errors'
import type {
  ChannelKey,
  Content,
  ContentPhase,
  ContentVersion,
  FieldError,
  FieldKey,
  Preview,
  VersionStatus,
  WorstCase,
} from '@/api/types'
import AbToggle from '@/components/AbToggle.vue'
import Button from '@/components/Button.vue'
import ChannelTabs from '@/components/ChannelTabs.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ContentEditor, { type EditConflict } from '@/components/ContentEditor.vue'
import EmailPreview from '@/components/EmailPreview.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorCard from '@/components/ErrorCard.vue'
import InlineError from '@/components/InlineError.vue'
import LanguageTabs from '@/components/LanguageTabs.vue'
import SmsPreview from '@/components/SmsPreview.vue'
import StepHeader from '@/components/StepHeader.vue'
import StreamingText from '@/components/StreamingText.vue'
import VariableChip from '@/components/VariableChip.vue'
import VariantTabs from '@/components/VariantTabs.vue'
import { useNotify } from '@/composables/useNotify'
import {
  FIELD_KEYS,
  type FieldState,
  routeFieldErrors,
  useContentGeneration,
} from '@/composables/useContentGeneration'
import type { MessageId } from '@/i18n'
import { t } from '@/i18n'
import { walkedSamples } from '@/lib/preview'
import { isLockedForEditing } from '@/lib/steps'
import { invalidVariables } from '@/lib/variables'
import { languageFromQuery } from '@/lib/language'
import {
  type ContentCopy,
  type ContentVariant,
  copyOf,
  isVariantBPath,
  isVariantLevelPath,
  variantFromQuery,
} from '@/lib/variant'
import { campaignIdOf } from '@/router'
import { injectApiClient, reportError } from '@/stores/api'
import { useCampaignStore } from '@/stores/campaign'
import { useConfigStore } from '@/stores/config'

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()
const config = useConfigStore()
const client = injectApiClient()
const { notify } = useNotify()
const gen = useContentGeneration()

const campaignId = computed(() => (route ? campaignIdOf(route) : null))
const campaign = computed(() =>
  campaignId.value && store.campaign?.id === campaignId.value ? store.campaign : null,
)
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))
const latest = computed(() => campaign.value?.latest_content_version ?? null)
const locked = computed(() => isLockedForEditing(campaign.value?.state ?? null))

// ---- editor state (ui.md §3.23) ----
/** Content the edit is based on (last validated version, or the invalid draft being fixed). */
const base = ref<Content | null>(null)
/** The editor's current text (always the full Content). */
const draft = ref<Content | null>(null)
/** Version the edit is based on: sent as X-Expected-Version (D40). */
const expectedVersion = ref<string>('none')
/** version_number of `expectedVersion`, to adopt only newer versions on a refetch. */
const knownNumber = ref<number | null>(null)
const worstCase = ref<WorstCase | null>(null)
const editingDraft = ref(false)
const saving = ref(false)
const fieldErrors = ref<FieldError[]>([])
const conflict = ref<EditConflict | null>(null)

/** Status of this page session; null => derived from the base version. */
const sessionStatus = ref<VersionStatus | null>(null)
const versionStatus = computed<VersionStatus>(
  () => sessionStatus.value ?? (base.value ? 'validated' : 'none'),
)

/** Field-by-field strict equality of two versions' texts (P8: English; P9: variant B, angles). */
function sameContent(a: Content, b: Content) {
  const same = (x: ContentCopy | null, y: ContentCopy | null) =>
    x === null || y === null
      ? x === y
      : x.sms.body === y.sms.body &&
        x.email.subject === y.email.subject &&
        x.email.body === y.email.body
  for (const v of ['a', 'b'] as const) {
    for (const l of ['tr', 'en'] as const) {
      if (!same(copyOf(a, v, l), copyOf(b, v, l))) return false
    }
  }
  return a.angles?.a === b.angles?.a && a.angles?.b === b.angles?.b
}

const dirty = computed(() => {
  if (editingDraft.value) return true
  const a = draft.value
  const b = base.value
  if (!a || !b) return false
  return !sameContent(a, b)
})

const channel = ref<ChannelKey>('sms')
type Language = 'tr' | 'en'
const language = ref<Language>(languageFromQuery(route.query.lang))
const langId = useId()

const showStream = computed(
  () =>
    !editingDraft.value &&
    (versionStatus.value === 'streaming' ||
      versionStatus.value === 'interrupted' ||
      versionStatus.value === 'invalid'),
)
const showEditor = computed(
  () => draft.value !== null && (versionStatus.value === 'validated' || editingDraft.value),
)

/** P8 (ui.md §3.23): English tabs/panels exist if the shown stream or the base version has `en`. */
const englishAvailable = computed(() =>
  showStream.value ? gen.hasEnglish.value : draft.value?.en != null,
)
const activeLanguage = computed<Language>(() => (englishAvailable.value ? language.value : 'tr'))

function onLanguage(l: Language) {
  language.value = l
  void router.replace({ query: { ...route.query, lang: l } })
}

// P9 (ui.md §3.23): variant tabs exist if the shown stream or the base version has variant B.
// Hierarchy: variant → language → channel. The `variant` query picks the first tab.
const variant = ref<ContentVariant>(variantFromQuery(route.query.variant))
const variantId = useId()
const variantBAvailable = computed(() =>
  showStream.value ? gen.hasVariantB.value : draft.value?.b != null,
)
const activeVariant = computed<ContentVariant>(() =>
  variantBAvailable.value ? variant.value : 'a',
)

function onVariant(v: ContentVariant) {
  variant.value = v
  void router.replace({ query: { ...route.query, variant: v } })
}

/** The stream's field set of one variant × language copy. */
function streamSet(v: ContentVariant, l: Language): Record<FieldKey, FieldState> {
  if (v === 'b') return l === 'en' ? gen.bEnFields : gen.bFields
  return l === 'en' ? gen.enFields : gen.fields
}

const phasesOf = (v: ContentVariant, l: Language): Record<FieldKey, ContentPhase> => {
  if (showStream.value || editingDraft.value) {
    const src = streamSet(v, l)
    return {
      'sms-body': src['sms-body'].phase,
      'email-subject': src['email-subject'].phase,
      'email-body': src['email-body'].phase,
    }
  }
  // Saved-version view: a 422 on a field marks it (and so its channel tab) invalid.
  const routed = routeFieldErrors(fieldErrors.value, l, v)
  const of = (k: FieldKey): ContentPhase => (routed[k].length ? 'invalid' : 'validated')
  return {
    'sms-body': of('sms-body'),
    'email-subject': of('email-subject'),
    'email-body': of('email-body'),
  }
}
const phases = computed(() => phasesOf(activeVariant.value, activeLanguage.value))

/** Summary phase (ui.md §3.27/§3.28): invalid if any field is, else the first field's phase. */
const summaryPhase = (p: Record<FieldKey, ContentPhase>): ContentPhase =>
  FIELD_KEYS.some((k) => p[k] === 'invalid') ? 'invalid' : p['sms-body']

/** Does one variant × language copy have a field error (stream or last save)? */
function copyHasErrors(v: ContentVariant, l: Language): boolean {
  if (showStream.value) return FIELD_KEYS.some((k) => streamSet(v, l)[k].errors.length > 0)
  const routed = routeFieldErrors(fieldErrors.value, l, v)
  return FIELD_KEYS.some((k) => routed[k].length > 0)
}

const languagePhases = computed<Record<Language, ContentPhase>>(() => ({
  tr: summaryPhase(phasesOf(activeVariant.value, 'tr')),
  en: summaryPhase(phasesOf(activeVariant.value, 'en')),
}))
const languageErrors = computed<Record<Language, boolean>>(() => ({
  tr: copyHasErrors(activeVariant.value, 'tr'),
  en: copyHasErrors(activeVariant.value, 'en'),
}))

/**
 * Variant-level errors (path `b`, `angles.*`): from the final `invalid` event while its fields
 * are shown, otherwise from the editor's errors (draft editing, 422 on save).
 */
const levelErrors = computed<FieldError[]>(() =>
  showStream.value
    ? gen.variantErrors.value
    : fieldErrors.value.filter((e) => isVariantLevelPath(e.path)),
)
const variantPhases = computed<Record<ContentVariant, ContentPhase>>(() => {
  const of = (v: ContentVariant): ContentPhase => {
    const langs: Language[] = englishAvailable.value ? ['tr', 'en'] : ['tr']
    const ps = langs.map((l) => summaryPhase(phasesOf(v, l)))
    return ps.includes('invalid') ? 'invalid' : (ps[0] as ContentPhase)
  }
  return { a: of('a'), b: of('b') }
})
const variantErrors = computed<Record<ContentVariant, boolean>>(() => {
  const has = (v: ContentVariant) =>
    copyHasErrors(v, 'tr') ||
    (englishAvailable.value && copyHasErrors(v, 'en')) ||
    levelErrors.value.some((e) => e.path === 'angles' || isVariantBPath(e.path) === (v === 'b'))
  return { a: has('a'), b: has('b') }
})

/** Angles shown under the tabs: the run's (validated only) or the edited version's. */
const shownAngles = computed(() =>
  showStream.value ? gen.angles.value : (draft.value?.angles ?? null),
)
const shownAngle = computed(() => shownAngles.value?.[activeVariant.value] || null)

/** Page-level A/B errors while the invalid draft is shown as StreamingText (ui.md §3.23). */
const pageVariantError = computed(() =>
  showStream.value && activeVariant.value === 'b'
    ? gen.variantErrors.value.find((e) => e.path === 'b')
    : undefined,
)
const pageAngleError = computed(() =>
  showStream.value
    ? gen.variantErrors.value.find(
        (e) => e.path === `angles.${activeVariant.value}` || e.path === 'angles',
      )
    : undefined,
)

const stateAllowsGeneration = computed(() => {
  const s = campaign.value?.state
  return s === 'audience_ready' || s === 'content_ready' || s === 'approved'
})
const canRegenerate = computed(
  () => versionStatus.value !== 'streaming' && stateAllowsGeneration.value && !locked.value,
)
// P9 (ui.md §3.23, §3.29): the A/B switch shapes the next generation. Starts on with `ab=1`
// (from the audience step) or when the latest version has variant B; a user choice wins.
const abChoice = ref<boolean | null>(route.query.ab === '1' ? true : null)
const ab = computed(() => abChoice.value ?? latest.value?.content.b != null)
function onAb(v: boolean) {
  if (canRegenerate.value) abChoice.value = v
}

const canApprove = computed(() => {
  const s = campaign.value?.state
  return (
    versionStatus.value === 'validated' &&
    !dirty.value &&
    (s === 'content_ready' || s === 'approved')
  )
})
const editorMode = computed(() => (locked.value ? 'locked' : 'editable'))

// Announced only on phase changes, never per delta (ui.md §3.23).
const liveText = computed(() => {
  if (sessionStatus.value === null) return ''
  const retrying = FIELD_KEYS.some((k) => gen.fields[k].phase === 'retrying')
  const key = retrying ? 'retrying' : sessionStatus.value
  if (key === 'none') return ''
  return t(`content.live.${key}` as MessageId)
})

// Step header (DESIGN.md §8.3): the content status badge and the next action.
const headerStatus = computed<string | null>(() => {
  const s = versionStatus.value
  if (s === 'none') return null
  if (s === 'streaming' || (s === 'validated' && dirty.value)) return 'draft'
  if (editingDraft.value) return 'draft'
  return s
})
const nextText = computed<string | null>(() => {
  if (locked.value) return null
  const s = versionStatus.value
  if (s === 'none') return t('content.next.generate')
  if (s === 'streaming') return t('content.next.streaming')
  if (dirty.value) return t('content.next.dirty')
  if (s === 'invalid') return t('content.next.invalid')
  if (s === 'interrupted') return t('content.next.interrupted')
  return canApprove.value ? t('content.next.toApproval') : null
})

const generationError = computed<ApiError | null>(() =>
  gen.error.value?.code === 'llm_unavailable' ? gen.error.value : null,
)

const invalidChips = computed(() => {
  const d = draft.value
  if (!d) return []
  const v = copyOf(d, activeVariant.value, activeLanguage.value) ?? d
  return channel.value === 'sms'
    ? invalidVariables(v.sms.body)
    : invalidVariables(v.email.subject, v.email.body)
})

// ---- preview (ui.md §1.1) ----
// Declared before the immediate `latest` watcher below, which may call loadPreview during
// setup when the campaign is already in the store (otherwise a TDZ ReferenceError).
const preview = ref<Preview | null>(null)
const previewLoading = ref(false)
const previewError = ref<ApiError | null>(null)
let previewSeq = 0
/** P8: `language` query of the last preview request; tabs visible => the selected tab's language. */
let requestedLanguage: Language | undefined
const previewLanguageParam = () => (englishAvailable.value ? activeLanguage.value : undefined)
/** P9: `variant` query, likewise only while the variant tabs are visible. */
let requestedVariant: ContentVariant | undefined
const previewVariantParam = () => (variantBAvailable.value ? activeVariant.value : undefined)

async function loadPreview(index: number) {
  const id = campaignId.value
  if (!id) return
  const seq = ++previewSeq
  requestedLanguage = previewLanguageParam()
  requestedVariant = previewVariantParam()
  previewLoading.value = true
  try {
    const p = await api.previewContent(client, id, index, requestedLanguage, requestedVariant)
    if (seq !== previewSeq) return
    preview.value = p
    previewError.value = null
  } catch (e) {
    if (seq !== previewSeq) return
    const err = toApiError(e)
    previewError.value = err
    if (err.code === 'customer_not_found' || err.code === 'network_error') reportError(err)
  } finally {
    if (seq === previewSeq) previewLoading.value = false
  }
}

// The preview follows the selected variant and language tabs (REQ-LNG-04, REQ-ABT-04);
// sample_index is kept.
watch([englishAvailable, activeLanguage, variantBAvailable, activeVariant], () => {
  if (
    preview.value &&
    (previewLanguageParam() !== requestedLanguage || previewVariantParam() !== requestedVariant)
  ) {
    void loadPreview(preview.value.sample_index)
  }
})

function onNextSample() {
  if (previewError.value?.code === 'customer_not_found') {
    void loadPreview(0)
    return
  }
  const p = preview.value
  if (!p || p.sample_count <= 1) return
  void loadPreview((p.sample_index + 1) % walkedSamples(p.sample_count))
}

// ---- base version handling ----
function adopt(v: ContentVersion) {
  base.value = v.content
  draft.value = v.content
  expectedVersion.value = v.id
  knownNumber.value = v.version_number
  worstCase.value = v.worst_case
}

// Adopt the latest version on load and when a refetch shows a newer one, unless the user
// has unsaved work (then the next save gets a 409 and the conflict panel decides).
watch(
  latest,
  (v) => {
    if (!v) return
    if (knownNumber.value !== null && v.version_number <= knownNumber.value) return
    if (dirty.value || conflict.value || versionStatus.value === 'streaming') return
    if (sessionStatus.value === 'invalid' || sessionStatus.value === 'interrupted') {
      // A finished failed run shows its draft; the stored latest version stays the base id.
      expectedVersion.value = v.id
      knownNumber.value = v.version_number
      return
    }
    const first = base.value === null
    adopt(v)
    if (first) void loadPreview(0)
  },
  { immediate: true },
)

// ---- generation (ui.md §5.2) ----
const confirmOpen = ref(false)

function startGeneration() {
  const id = campaignId.value
  if (!id) return
  editingDraft.value = false
  conflict.value = null
  fieldErrors.value = []
  sessionStatus.value = 'streaming'
  void gen.start(id, { ab: ab.value })
}

function onRegenerate() {
  if (!canRegenerate.value) return
  const hasValidated = latest.value !== null || gen.versionStatus.value === 'validated'
  if (hasValidated || dirty.value) confirmOpen.value = true
  else startGeneration()
}

function onConfirmRegenerate() {
  confirmOpen.value = false
  startGeneration()
}

const copyFrom = (f: Record<FieldKey, FieldState>): ContentCopy => ({
  sms: { body: f['sms-body'].text },
  email: { subject: f['email-subject'].text, body: f['email-body'].text },
})

/**
 * The generated texts as a Content: the English block and variant B (with its angles) only
 * when the run had them. Angles are not streamed: `angles` is the validated event's, else empty.
 */
function generatedContent(angles: { a: string; b: string } | null = gen.angles.value): Content {
  const c: Content = copyFrom(gen.fields)
  if (gen.hasEnglish.value) c.en = copyFrom(gen.enFields)
  if (gen.hasVariantB.value) {
    c.b = copyFrom(gen.bFields)
    if (gen.hasEnglish.value) c.b.en = copyFrom(gen.bEnFields)
    c.angles = { ...(angles ?? { a: '', b: '' }) }
  }
  return c
}

watch(gen.versionStatus, (s, prev) => {
  if (s === prev) return
  if (s === 'none') {
    // The request failed before the stream opened: fall back to the base version.
    sessionStatus.value = null
    return
  }
  sessionStatus.value = s
  if (s === 'validated') {
    const c = generatedContent()
    base.value = c
    draft.value = c
    worstCase.value = gen.worstCase.value
    if (gen.versionId.value) expectedVersion.value = gen.versionId.value
    fieldErrors.value = []
    void refetch()
    void loadPreview(0)
  }
})

watch(gen.error, (err) => {
  if (!err) return
  if (err.code === 'llm_unavailable') return // ErrorCard action="regenerate"
  reportError(err, 'content')
  if (err.status === 409 || err.status === 404) void refetch()
})

function onEditDraft() {
  if (versionStatus.value !== 'invalid' || locked.value) return
  const c = generatedContent({ a: '', b: '' })
  // Errors with path "" sit in every field; keep each error object once.
  const seen = new Set<FieldError>()
  for (const set of [gen.fields, gen.enFields, gen.bFields, gen.bEnFields]) {
    for (const k of FIELD_KEYS) for (const e of set[k].errors) seen.add(e)
  }
  for (const e of gen.variantErrors.value) seen.add(e)
  base.value = c
  draft.value = c
  fieldErrors.value = [...seen]
  worstCase.value = null
  editingDraft.value = true
  if (c.b) void loadDraftAngles(gen.versionId.value)
}

/**
 * P9 (ui.md §3.23): the stored draft's angles (they never stream). No draft id, no `angles` in
 * the version or a failed read leaves them empty; saving then reports `required`.
 */
async function loadDraftAngles(versionId: string | null) {
  const id = campaignId.value
  if (!id || !versionId) return
  let angles: { a: string; b: string } | undefined
  try {
    angles = (await api.getContentVersion(client, id, versionId)).content.angles
  } catch (e) {
    reportError(toApiError(e), 'content')
    return
  }
  // Only while that draft is still being edited and its angles are untouched.
  const d = draft.value
  if (!angles || !editingDraft.value || !d?.b || d.angles?.a || d.angles?.b) return
  const next = { ...d, angles: { a: angles.a, b: angles.b } }
  draft.value = next
  if (base.value?.b) base.value = { ...base.value, angles: next.angles }
}

// ---- saving (ui.md §3.23, D40) ----
function savedAs(v: ContentVersion) {
  adopt(v)
  fieldErrors.value = []
  conflict.value = null
  editingDraft.value = false
  sessionStatus.value = 'validated'
  notify({ variant: 'success', messageId: 'toast.contentSaved' })
  void loadPreview(0)
}

async function refetch() {
  const id = campaignId.value
  if (id) await store.load(id)
}

const pageEl = ref<HTMLElement | null>(null)

function focusFirstInvalid() {
  void nextTick(() => {
    pageEl.value?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()
  })
}

async function onSave(c: Content) {
  const id = campaignId.value
  if (!id || saving.value || conflict.value) return
  saving.value = true
  try {
    const v = await api.editContent(client, id, c, expectedVersion.value)
    savedAs(v)
    await refetch()
  } catch (e) {
    const err = toApiError(e)
    await onSaveError(err, c)
  } finally {
    saving.value = false
  }
}

async function onSaveError(err: ApiError, mine: Content) {
  if (err.code === 'content_edit_conflict') {
    await refetch()
    const now = latest.value
    if (now && sameContent(now.content, mine)) {
      // A retried request that had already succeeded (D40): treat it as success.
      savedAs(now)
      return
    }
    // ui.md §3.23: conflict := error.current_version; the refetched latest is only a fallback
    // when the 409 carried none.
    const cur = err.currentVersion
    conflict.value = cur
      ? { currentVersionId: cur.id, currentVersionNumber: cur.version_number }
      : { currentVersionId: now?.id ?? null, currentVersionNumber: now?.version_number ?? null }
    return
  }
  if (err.code === 'validation_failed') {
    const header = err.fields.filter((f) => f.path.startsWith('header.'))
    if (header.length) notify({ variant: 'error', messageId: 'error.validation_failed' })
    fieldErrors.value = err.fields.filter((f) => !f.path.startsWith('header.'))
    focusFirstInvalid()
    return
  }
  if (err.code === 'payload_too_large') {
    fieldErrors.value = [{ path: '', code: 'payload_too_large', message: '' }]
    focusFirstInvalid()
    return
  }
  reportError(err, 'content')
  if (err.status === 409 || err.status === 404) await refetch()
}

function onReloadLatest() {
  const v = latest.value
  conflict.value = null
  fieldErrors.value = []
  editingDraft.value = false
  if (v) {
    adopt(v)
    sessionStatus.value = 'validated'
  }
  void loadPreview(0)
}

function onKeepMine() {
  // ui.md §3.23: rebase onto the version the 409 named, not the (possibly failed) refetch.
  const c = conflict.value
  if (!c) return
  conflict.value = null
  expectedVersion.value = c.currentVersionId ?? 'none'
  knownNumber.value = c.currentVersionNumber
  const v = latest.value
  if (v && v.id === c.currentVersionId) base.value = v.content
}

function onDraftUpdate(c: Content) {
  draft.value = c
}

function goToApproval() {
  const id = campaignId.value
  if (!id || !canApprove.value) return
  // Carries the selected language and variant (only while their tabs are shown).
  const query: Record<string, string> = {}
  if (englishAvailable.value) query.lang = activeLanguage.value
  if (variantBAvailable.value) query.variant = activeVariant.value
  void router.push({
    name: 'approval',
    params: { campaignId: id },
    ...(Object.keys(query).length ? { query } : {}),
  })
}

const CHANNELS: readonly ChannelKey[] = ['sms', 'email']
/**
 * Every variant × language copy of the stream, the shown one first: the others stay in the DOM
 * (hidden) so streaming keeps filling them, and the first match of a field's testid is the
 * visible one.
 */
const streamCopies = computed(() => {
  const vs: ContentVariant[] = variantBAvailable.value ? ['a', 'b'] : ['a']
  const ls: Language[] = englishAvailable.value ? ['tr', 'en'] : ['tr']
  const all = vs.flatMap((v) => ls.map((l) => ({ v, l, key: `${v}-${l}` })))
  const shown = (x: { v: ContentVariant; l: Language }) =>
    x.v === activeVariant.value && x.l === activeLanguage.value
  return [...all.filter(shown), ...all.filter((x) => !shown(x))]
})
const streamFields: Record<ChannelKey, readonly FieldKey[]> = {
  sms: ['sms-body'],
  email: ['email-subject', 'email-body'],
}

onMounted(() => {
  const id = campaignId.value
  if (!id) return
  void store.ensure(id)
  if (route.query.generate === '1') {
    const rest = { ...route.query }
    delete rest.generate
    delete rest.ab
    void router.replace({ query: rest })
    startGeneration()
  }
})
</script>

<template>
  <div ref="pageEl" data-testid="page-content" class="mx-auto flex w-full max-w-5xl flex-col gap-6">
    <EmptyState
      v-if="notFound"
      kind="campaign_not_found"
      @action="router.push({ name: 'brief-new' })"
    />
    <template v-else>
      <StepHeader
        step="content"
        :role="t('content.role')"
        :title="t('content.title')"
        :subtitle="t('content.subtitle')"
        status-kind="content"
        :status="headerStatus"
        :next="nextText"
      />

      <p data-testid="content-live-status" role="status" aria-live="polite" class="sr-only">
        {{ liveText }}
      </p>

      <ErrorCard
        v-if="generationError"
        :error="generationError"
        action="regenerate"
        context="content"
        @action="startGeneration"
      />

      <section
        v-if="versionStatus === 'none'"
        data-testid="content-empty"
        class="flex flex-col items-center gap-4 rounded-card border border-border-default bg-surface-card p-8 text-center shadow-card"
      >
        <Sparkles class="size-12 text-brand-icon" aria-hidden="true" />
        <p class="text-body text-ink-700">{{ t('content.empty') }}</p>
        <div class="flex flex-col items-center gap-4 sm:flex-row">
          <div class="text-left sm:max-w-64">
            <AbToggle
              data-testid="content-ab-toggle"
              :model-value="ab"
              :disabled="!canRegenerate"
              @update:model-value="onAb"
            />
          </div>
          <Button
            data-testid="content-regenerate"
            size="lg"
            :disabled="!canRegenerate"
            @click="onRegenerate"
          >
            {{ t('content.generate') }}
            <ArrowUpRight aria-hidden="true" />
          </Button>
        </div>
      </section>

      <template v-else>
        <!-- P9: variant → language → channel. The angle names what the selected variant tries. -->
        <div v-if="variantBAvailable" class="flex flex-col gap-2">
          <VariantTabs
            :model-value="activeVariant"
            :phases="variantPhases"
            :errors="variantErrors"
            :id-base="variantId"
            @update:model-value="onVariant"
          />
          <p
            v-if="shownAngle"
            class="flex items-start gap-2 text-body-sm text-ink-700"
            :aria-label="t('variant.angle')"
          >
            <FlaskConical class="mt-0.5 size-4 shrink-0 text-ink-500" aria-hidden="true" />
            <span
              data-testid="content-variant-angle"
              class="min-w-0 break-words"
              v-text="shownAngle"
            />
          </p>
          <InlineError
            v-if="pageVariantError"
            path="variant"
            :code="pageVariantError.code"
            class="rounded-sm bg-error-bg px-3 py-2"
          />
          <InlineError
            v-if="pageAngleError"
            path="angle"
            :code="pageAngleError.code"
            :limit="pageAngleError.limit ?? null"
            :actual="pageAngleError.actual ?? null"
            class="rounded-sm bg-error-bg px-3 py-2"
          />
        </div>
        <div
          :id="variantBAvailable ? `${variantId}-panel` : undefined"
          :role="variantBAvailable ? 'tabpanel' : undefined"
          :aria-labelledby="variantBAvailable ? `${variantId}-tab-${activeVariant}` : undefined"
          class="flex flex-col gap-6"
        >
          <LanguageTabs
            v-if="englishAvailable"
            :model-value="activeLanguage"
            :phases="languagePhases"
            :errors="languageErrors"
            :id-base="langId"
            @update:model-value="onLanguage"
          />
          <div
            :id="englishAvailable ? `${langId}-panel` : undefined"
            :role="englishAvailable ? 'tabpanel' : undefined"
            :aria-labelledby="englishAvailable ? `${langId}-tab-${activeLanguage}` : undefined"
          >
            <ChannelTabs v-model="channel" :phases="phases">
              <template v-for="c in CHANNELS" #[c]>
                <div v-if="showStream" :key="`stream-${c}`" class="flex flex-col gap-4">
                  <div
                    v-for="x in streamCopies"
                    :key="x.key"
                    :data-variant="x.v"
                    :data-language="x.l"
                    :hidden="x.v !== activeVariant || x.l !== activeLanguage"
                    class="flex flex-col gap-4"
                  >
                    <StreamingText
                      v-for="f in streamFields[c]"
                      :key="f"
                      :field="f"
                      :text="streamSet(x.v, x.l)[f].text"
                      :phase="streamSet(x.v, x.l)[f].phase"
                      :errors="streamSet(x.v, x.l)[f].errors"
                      :provider="config.provider"
                      @regenerate="onRegenerate"
                    />
                  </div>
                </div>
                <div
                  v-else-if="showEditor && draft && channel === c"
                  :key="`editor-${c}`"
                  class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]"
                >
                  <div
                    class="rounded-card border border-border-default bg-surface-card p-4 shadow-card md:p-6"
                  >
                    <ContentEditor
                      :model-value="draft"
                      :channel="c"
                      :language="activeLanguage"
                      :variant="activeVariant"
                      :mode="editorMode"
                      :dirty="dirty"
                      :saving="saving"
                      :field-errors="fieldErrors"
                      :worst-case="dirty ? null : worstCase"
                      :conflict="conflict"
                      @update:model-value="onDraftUpdate"
                      @save="onSave"
                      @reload-latest="onReloadLatest"
                      @keep-mine="onKeepMine"
                    />
                  </div>
                  <aside
                    class="flex flex-col gap-3 rounded-card border border-dashed border-border-default bg-surface-sunken/60 p-4 md:p-6"
                    :aria-label="t('content.previewTitle')"
                  >
                    <div class="flex flex-col gap-0.5">
                      <h2 class="text-h4 text-accent-heading">{{ t('content.previewTitle') }}</h2>
                      <p class="text-caption text-ink-500">{{ t('content.previewHint') }}</p>
                    </div>
                    <p
                      v-if="preview?.fallback"
                      data-testid="preview-fallback-note"
                      role="note"
                      class="flex items-start gap-2 rounded-sm bg-info-bg px-3 py-2 text-body-sm text-ink-700"
                    >
                      <Info class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                      {{ t('preview.fallbackNote') }}
                    </p>
                    <SmsPreview
                      v-if="c === 'sms'"
                      :preview="preview"
                      :loading="previewLoading"
                      :error="previewError"
                      @next-sample="onNextSample"
                    />
                    <EmailPreview
                      v-else
                      :preview="preview"
                      :loading="previewLoading"
                      :error="previewError"
                    />
                    <div v-if="invalidChips.length" class="flex flex-col gap-1.5">
                      <p class="text-caption text-ink-500">{{ t('editor.invalidVariables') }}</p>
                      <div class="flex flex-wrap gap-2">
                        <VariableChip
                          v-for="v in invalidChips"
                          :key="v"
                          :name="v"
                          variant="invalid"
                        />
                      </div>
                    </div>
                  </aside>
                </div>
              </template>
            </ChannelTabs>
          </div>
        </div>

        <p
          v-if="versionStatus === 'invalid' && !editingDraft"
          class="rounded-sm bg-error-bg px-3 py-2 text-body-sm text-error-strong"
        >
          {{ t('content.invalidNote') }}
        </p>

        <div
          role="group"
          :aria-label="t('content.actions')"
          class="flex flex-col gap-3 rounded-card border border-border-default bg-surface-card p-4 shadow-card md:flex-row md:items-center md:justify-between"
        >
          <div class="flex flex-wrap items-center gap-3">
            <div class="w-full sm:w-auto sm:max-w-64">
              <AbToggle
                data-testid="content-ab-toggle"
                :model-value="ab"
                :disabled="!canRegenerate"
                @update:model-value="onAb"
              />
            </div>
            <Button
              data-testid="content-regenerate"
              :variant="
                versionStatus === 'invalid' || versionStatus === 'interrupted'
                  ? 'primary'
                  : 'secondary'
              "
              :disabled="!canRegenerate"
              @click="onRegenerate"
            >
              <RotateCcw aria-hidden="true" />
              {{ t('content.regenerate') }}
            </Button>
            <Button
              v-if="versionStatus === 'invalid' && !editingDraft"
              data-testid="content-edit-draft"
              variant="secondary"
              :disabled="locked"
              @click="onEditDraft"
            >
              <PencilLine aria-hidden="true" />
              {{ t('content.editDraft') }}
            </Button>
          </div>
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center md:ml-auto">
            <p v-if="canApprove" class="flex items-center gap-1.5 text-body-sm text-ink-600">
              <CircleCheck class="size-4 shrink-0 text-success" aria-hidden="true" />
              {{ t('content.readyNote') }}
            </p>
            <p
              v-else-if="dirty && versionStatus === 'validated'"
              class="flex items-center gap-1.5 text-body-sm text-ink-600"
            >
              <Info class="size-4 shrink-0" aria-hidden="true" />
              {{ t('content.dirtyNote') }}
            </p>
            <Button
              data-testid="content-to-approval"
              size="lg"
              :variant="canApprove ? 'primary' : 'secondary'"
              :disabled="!canApprove"
              @click="goToApproval"
            >
              {{ t('content.toApproval') }}
              <ArrowUpRight aria-hidden="true" />
            </Button>
          </div>
        </div>
      </template>

      <ConfirmDialog
        v-model:open="confirmOpen"
        kind="regenerate"
        @confirm="onConfirmRegenerate"
        @cancel="confirmOpen = false"
      />
    </template>
  </div>
</template>
