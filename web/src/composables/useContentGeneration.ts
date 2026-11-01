// Content generation over POST + fetch stream reader (docs/contracts/ui.md §5.2,
// sse-events.md §1.2–§1.3.1). EventSource cannot POST, so the body is read with a
// ReadableStream reader and parsed with createSSEParser. No automatic reconnect (D42).
import { getCurrentScope, inject, onScopeDispose, reactive, ref } from 'vue'

import { FetchKey } from '@/api/client'
import type { ApiError } from '@/api/errors'
import { apiErrorFromBody, networkError } from '@/api/errors'
import { createSSEParser, type SSEMessage } from '@/api/sse'
import type {
  Content,
  ContentInvalidEvent,
  ContentPhase,
  ContentValidatedEvent,
  FieldError,
  FieldKey,
  VersionStatus,
  WorstCase,
} from '@/api/types'
import { type ContentCopy, isVariantLevelPath } from '@/lib/variant'

export interface FieldState {
  text: string
  phase: ContentPhase
  errors: FieldError[]
}

export const FIELD_KEYS: readonly FieldKey[] = ['sms-body', 'email-subject', 'email-body']

/** API error path -> field key (ui.md §0). `""` and unknown paths belong to every field. */
export function fieldKeyOfPath(path: string): FieldKey | null {
  switch (path) {
    case 'sms.body':
      return 'sms-body'
    case 'email.subject':
      return 'email-subject'
    case 'email.body':
      return 'email-body'
    default:
      return null
  }
}

const isEnPath = (path: string) => path === 'en' || path.startsWith('en.')

/**
 * Splits field errors by field for one variant × language (P8/P9, ui.md §3.14, §5.2); `""` (and
 * unmapped) paths are added to every field of every copy. Variant A takes the unprefixed and
 * `en.` paths, variant B the `b.` and `b.en.` ones; within a variant `en.` belongs to English
 * only. Variant-level paths (`b`, `angles.*`) belong to no field (`variantErrors`).
 */
export function routeFieldErrors(
  errors: readonly FieldError[],
  language: 'tr' | 'en' = 'tr',
  variant: 'a' | 'b' = 'a',
): Record<FieldKey, FieldError[]> {
  const out: Record<FieldKey, FieldError[]> = {
    'sms-body': [],
    'email-subject': [],
    'email-body': [],
  }
  for (const e of errors) {
    if (isVariantLevelPath(e.path)) continue
    const isB = e.path.startsWith('b.')
    if (e.path !== '' && isB !== (variant === 'b')) continue
    const path = isB ? e.path.slice(2) : e.path
    const en = isEnPath(path)
    if (language === 'tr' && en) continue
    const key = fieldKeyOfPath(language === 'en' && en ? path.slice(3) : path)
    if (key) {
      if ((language === 'en') === en) out[key].push(e)
    } else for (const k of FIELD_KEYS) out[k].push(e)
  }
  return out
}

const DELTA_FIELD: Record<string, FieldKey> = {
  'sms:body': 'sms-body',
  'email:subject': 'email-subject',
  'email:body': 'email-body',
}

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null
const isAttempt = (v: unknown) => v === 1 || v === 2

function isFieldError(v: unknown): v is FieldError {
  return isObj(v) && typeof v.path === 'string' && typeof v.code === 'string'
}

function asInvalid(d: unknown): ContentInvalidEvent | null {
  if (!isObj(d) || !isAttempt(d.attempt) || typeof d.final !== 'boolean') return null
  if (!Array.isArray(d.errors) || !d.errors.every(isFieldError)) return null
  if (d.content_version_id !== null && typeof d.content_version_id !== 'string') return null
  return d as unknown as ContentInvalidEvent
}

/** sms.body, email.subject, email.body are strings (one variant × language copy). */
function isCopy(v: unknown): boolean {
  if (!isObj(v) || !isObj(v.sms) || !isObj(v.email)) return false
  return (
    typeof v.sms.body === 'string' &&
    typeof v.email.subject === 'string' &&
    typeof v.email.body === 'string'
  )
}

function asValidated(d: unknown): ContentValidatedEvent | null {
  if (!isObj(d) || !isAttempt(d.attempt) || typeof d.content_version_id !== 'string') return null
  if (typeof d.version_number !== 'number') return null
  const c = d.content
  if (!isObj(c) || !isCopy(c)) return null
  if (c.en !== undefined && !isCopy(c.en)) return null
  // P9: variant B with its own optional English copy, and the two angle labels.
  if (c.b !== undefined) {
    if (!isObj(c.b) || !isCopy(c.b)) return null
    if (c.b.en !== undefined && !isCopy(c.b.en)) return null
  }
  if (c.angles !== undefined) {
    const a = c.angles
    if (!isObj(a) || typeof a.a !== 'string' || typeof a.b !== 'string') return null
  }
  const wc = d.worst_case
  if (!isObj(wc) || typeof wc.sms_runes !== 'number' || typeof wc.subject_runes !== 'number') {
    return null
  }
  return d as unknown as ContentValidatedEvent
}

export function useContentGeneration() {
  const injected = inject(FetchKey, null)
  const doFetch: typeof fetch = injected ?? ((...args) => globalThis.fetch(...args))

  const fields = reactive<Record<FieldKey, FieldState>>({
    'sms-body': { text: '', phase: 'idle', errors: [] },
    'email-subject': { text: '', phase: 'idle', errors: [] },
    'email-body': { text: '', phase: 'idle', errors: [] },
  })
  /** P8: English version; stays idle/empty when the run has no `language: "en"` chunks. */
  const enFields = reactive<Record<FieldKey, FieldState>>({
    'sms-body': { text: '', phase: 'idle', errors: [] },
    'email-subject': { text: '', phase: 'idle', errors: [] },
    'email-body': { text: '', phase: 'idle', errors: [] },
  })
  const hasEnglish = ref(false)
  /** P9: variant B (Turkish and English); idle/empty unless the run has `variant: "b"` chunks. */
  const bFields = reactive<Record<FieldKey, FieldState>>({
    'sms-body': { text: '', phase: 'idle', errors: [] },
    'email-subject': { text: '', phase: 'idle', errors: [] },
    'email-body': { text: '', phase: 'idle', errors: [] },
  })
  const bEnFields = reactive<Record<FieldKey, FieldState>>({
    'sms-body': { text: '', phase: 'idle', errors: [] },
    'email-subject': { text: '', phase: 'idle', errors: [] },
    'email-body': { text: '', phase: 'idle', errors: [] },
  })
  const hasVariantB = ref(false)
  /** P9: angles come only with `validated.content.angles` (they are not streamed). */
  const angles = ref<{ a: string; b: string } | null>(null)
  /** P9: final `invalid` errors with path `b` (variants_identical) and `angles.*`. */
  const variantErrors = ref<FieldError[]>([])
  /** The `validated` event's content as sent (null until a run validates). */
  const content = ref<Content | null>(null)
  const versionStatus = ref<VersionStatus>('none')
  const versionId = ref<string | null>(null)
  const worstCase = ref<WorstCase | null>(null)
  const attempt = ref(1)
  const error = ref<ApiError | null>(null)

  let controller: AbortController | null = null
  let reader: ReadableStreamDefaultReader<Uint8Array> | null = null
  /** Bumped by every start/abort/dispose: a run whose token is stale touches nothing. */
  let run = 0
  let disposed = false

  /** The field sets in play: English and variant B only once they have shown up. */
  const groups = () => {
    const g = [fields]
    if (hasEnglish.value) g.push(enFields)
    if (hasVariantB.value) {
      g.push(bFields)
      if (hasEnglish.value) g.push(bEnFields)
    }
    return g
  }

  function setAll(phase: ContentPhase, clearText: boolean) {
    for (const g of groups()) {
      for (const k of FIELD_KEYS) {
        g[k].phase = phase
        g[k].errors = []
        if (clearText) g[k].text = ''
      }
    }
  }

  function interrupt() {
    for (const g of groups()) {
      for (const k of FIELD_KEYS) {
        if (g[k].phase !== 'validated' && g[k].phase !== 'invalid') g[k].phase = 'interrupted'
      }
    }
    versionStatus.value = 'interrupted'
  }

  function stopTransport() {
    controller?.abort()
    controller = null
    if (reader) {
      reader.cancel().catch(() => {})
      reader = null
    }
  }

  async function start(campaignId: string, opts: { ab?: boolean } = {}): Promise<void> {
    if (disposed || versionStatus.value === 'streaming') return
    const token = ++run
    const live = () => token === run && !disposed

    setAll('streaming', true)
    for (const g of [enFields, bFields, bEnFields]) {
      for (const k of FIELD_KEYS) {
        g[k].phase = 'idle'
        g[k].errors = []
        g[k].text = ''
      }
    }
    hasEnglish.value = false
    hasVariantB.value = false
    angles.value = null
    variantErrors.value = []
    content.value = null
    versionStatus.value = 'streaming'
    versionId.value = null
    worstCase.value = null
    attempt.value = 1
    error.value = null

    const ctrl = new AbortController()
    controller = ctrl
    const url = `/api/campaigns/${encodeURIComponent(campaignId)}/content`

    let res: Response
    try {
      res = await doFetch(url, {
        method: 'POST',
        headers: { Accept: 'text/event-stream', 'Content-Type': 'application/json' },
        // P9: `{"ab":true}` asks for variant B; otherwise the exact pre-P9 bytes.
        body: opts.ab ? '{"ab":true}' : '{}',
        signal: ctrl.signal,
      })
    } catch (e) {
      if (!live()) return
      if (ctrl.signal.aborted) {
        interrupt()
        return
      }
      error.value = networkError(e)
      setAll('idle', true)
      versionStatus.value = 'none'
      return
    }
    if (!live()) return

    if (!res.ok) {
      const requestId = res.headers?.get?.('X-Request-ID') ?? null
      const text = await res.text().catch(() => '')
      if (!live()) return
      let body: unknown = undefined
      try {
        body = text ? JSON.parse(text) : undefined
      } catch {
        body = undefined
      }
      error.value = apiErrorFromBody(res.status, body, requestId)
      setAll('idle', true)
      versionStatus.value = 'none'
      return
    }

    if (!res.body) {
      interrupt()
      return
    }

    let terminal = false
    let broken = false

    const onMessage = (m: SSEMessage) => {
      if (terminal || broken || !live()) return
      if (m.event !== 'delta' && m.event !== 'invalid' && m.event !== 'validated') return
      let data: unknown
      try {
        data = JSON.parse(m.data)
      } catch {
        broken = true
        return
      }
      if (m.event === 'delta') {
        if (!isObj(data) || !isAttempt(data.attempt) || typeof data.text !== 'string') {
          broken = true
          return
        }
        attempt.value = data.attempt as number
        const key = DELTA_FIELD[`${String(data.channel)}:${String(data.field)}`]
        if (!key) return
        if (data.variant !== undefined && data.variant !== 'b') return
        const en = data.language === 'en'
        const b = data.variant === 'b'
        const target = b ? (en ? bEnFields : bFields) : en ? enFields : fields
        if (en) hasEnglish.value = true
        if (b) hasVariantB.value = true
        target[key].text += data.text
        target[key].phase = 'streaming'
      } else if (m.event === 'invalid') {
        const ev = asInvalid(data)
        if (!ev) {
          broken = true
          return
        }
        attempt.value = ev.attempt
        if (!ev.final) {
          setAll('retrying', true)
          return
        }
        terminal = true
        // A malformed `en` / `b` block (no such deltas) still needs its tab and errors. The bare
        // `b.en` parity error is no reason for English: with `en` absent it means `b.en` must go.
        if (ev.errors.some((e) => isEnPath(e.path) || e.path.startsWith('b.en.'))) {
          hasEnglish.value = true
        }
        if (ev.errors.some((e) => e.path === 'b' || e.path.startsWith('b.'))) {
          hasVariantB.value = true
        }
        if (ev.errors.some((e) => isVariantLevelPath(e.path))) hasVariantB.value = true
        variantErrors.value = ev.errors.filter((e) => isVariantLevelPath(e.path))
        const sets: [Record<FieldKey, FieldState>, 'tr' | 'en', 'a' | 'b', boolean][] = [
          [fields, 'tr', 'a', true],
          [enFields, 'en', 'a', hasEnglish.value],
          [bFields, 'tr', 'b', hasVariantB.value],
          [bEnFields, 'en', 'b', hasEnglish.value && hasVariantB.value],
        ]
        for (const [g, language, variant, used] of sets) {
          if (!used) continue
          const routed = routeFieldErrors(ev.errors, language, variant)
          for (const k of FIELD_KEYS) {
            g[k].phase = 'invalid'
            g[k].errors = routed[k]
          }
        }
        versionId.value = ev.content_version_id
        versionStatus.value = 'invalid'
      } else {
        const ev = asValidated(data)
        if (!ev) {
          broken = true
          return
        }
        terminal = true
        attempt.value = ev.attempt
        // The validated content is the source of truth: a repair attempt may drop `en`/`b` that
        // an earlier attempt streamed, so the flags and field sets follow `ev.content` alone.
        const c = ev.content
        const fill = (g: Record<FieldKey, FieldState>, copy: ContentCopy | undefined) => {
          const text: Record<FieldKey, string> = {
            'sms-body': copy?.sms.body ?? '',
            'email-subject': copy?.email.subject ?? '',
            'email-body': copy?.email.body ?? '',
          }
          for (const k of FIELD_KEYS) {
            g[k].text = text[k]
            if (!copy) {
              g[k].phase = 'idle'
              g[k].errors = []
            }
          }
        }
        hasEnglish.value = c.en != null || c.b?.en != null
        hasVariantB.value = c.b != null
        fill(fields, c)
        fill(enFields, c.en)
        fill(bFields, c.b)
        fill(bEnFields, c.b?.en)
        content.value = c
        angles.value = c.angles ? { ...c.angles } : null
        setAll('validated', false)
        versionId.value = ev.content_version_id
        worstCase.value = ev.worst_case
        versionStatus.value = 'validated'
      }
    }

    const parser = createSSEParser(onMessage)
    const decoder = new TextDecoder()
    const r = res.body.getReader()
    reader = r
    try {
      for (;;) {
        const { done, value } = await r.read()
        if (done || !live()) break
        parser.push(decoder.decode(value, { stream: true }))
        if (broken) {
          // Data that breaks the event schema counts as "no terminal event" (sse-events.md §1.3.1).
          r.cancel().catch(() => {})
          break
        }
      }
      if (live() && !broken) parser.push(decoder.decode())
      parser.end()
    } catch {
      // A network error after 200 is an interruption, not an ApiError.
    }
    if (!live()) return
    if (reader === r) reader = null
    if (controller === ctrl) controller = null
    if (!terminal) interrupt()
  }

  function abort() {
    if (disposed) return
    const wasStreaming = versionStatus.value === 'streaming'
    run++
    stopTransport()
    if (wasStreaming) interrupt()
  }

  if (getCurrentScope()) {
    onScopeDispose(() => {
      disposed = true
      run++
      stopTransport()
    })
  }

  return {
    start,
    abort,
    fields,
    enFields,
    hasEnglish,
    bFields,
    bEnFields,
    hasVariantB,
    angles,
    variantErrors,
    content,
    versionStatus,
    versionId,
    worstCase,
    attempt,
    error,
  }
}
