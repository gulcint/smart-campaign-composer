// Content generation over POST + fetch stream reader (docs/contracts/ui.md §5.2,
// sse-events.md §1.2–§1.3.1). EventSource cannot POST, so the body is read with a
// ReadableStream reader and parsed with createSSEParser. No automatic reconnect (D42).
import { getCurrentScope, inject, onScopeDispose, reactive, ref } from 'vue'

import { FetchKey } from '@/api/client'
import type { ApiError } from '@/api/errors'
import { apiErrorFromBody, networkError } from '@/api/errors'
import { createSSEParser, type SSEMessage } from '@/api/sse'
import type {
  ContentInvalidEvent,
  ContentPhase,
  ContentValidatedEvent,
  FieldError,
  FieldKey,
  VersionStatus,
  WorstCase,
} from '@/api/types'

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
 * Splits field errors by field for one language (P8, ui.md §5.2); `""` (and unmapped) paths
 * are added to every field. `en.`-prefixed paths belong to `en` only, other mapped paths to `tr`.
 */
export function routeFieldErrors(
  errors: readonly FieldError[],
  language: 'tr' | 'en' = 'tr',
): Record<FieldKey, FieldError[]> {
  const out: Record<FieldKey, FieldError[]> = {
    'sms-body': [],
    'email-subject': [],
    'email-body': [],
  }
  for (const e of errors) {
    const en = isEnPath(e.path)
    if (language === 'tr' && en) continue
    const key = fieldKeyOfPath(language === 'en' && en ? e.path.slice(3) : e.path)
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

function asValidated(d: unknown): ContentValidatedEvent | null {
  if (!isObj(d) || !isAttempt(d.attempt) || typeof d.content_version_id !== 'string') return null
  if (typeof d.version_number !== 'number') return null
  const c = d.content
  if (!isObj(c) || !isObj(c.sms) || !isObj(c.email)) return null
  if (typeof c.sms.body !== 'string') return null
  if (typeof c.email.subject !== 'string' || typeof c.email.body !== 'string') return null
  if (c.en !== undefined) {
    const en = c.en
    if (!isObj(en) || !isObj(en.sms) || !isObj(en.email)) return null
    if (typeof en.sms.body !== 'string') return null
    if (typeof en.email.subject !== 'string' || typeof en.email.body !== 'string') return null
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

  /** The field sets of the languages in play: English only once it has shown up. */
  const groups = () => (hasEnglish.value ? [fields, enFields] : [fields])

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

  async function start(campaignId: string): Promise<void> {
    if (disposed || versionStatus.value === 'streaming') return
    const token = ++run
    const live = () => token === run && !disposed

    setAll('streaming', true)
    for (const k of FIELD_KEYS) {
      enFields[k].phase = 'idle'
      enFields[k].errors = []
      enFields[k].text = ''
    }
    hasEnglish.value = false
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
        body: '{}',
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
        const target = data.language === 'en' ? enFields : fields
        if (data.language === 'en') hasEnglish.value = true
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
        // A malformed `en` block (no English deltas) still needs its tab and errors.
        if (ev.errors.some((e) => isEnPath(e.path))) hasEnglish.value = true
        const routed = routeFieldErrors(ev.errors)
        const routedEn = routeFieldErrors(ev.errors, 'en')
        for (const k of FIELD_KEYS) {
          fields[k].phase = 'invalid'
          fields[k].errors = routed[k]
          if (hasEnglish.value) {
            enFields[k].phase = 'invalid'
            enFields[k].errors = routedEn[k]
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
        fields['sms-body'].text = ev.content.sms.body
        fields['email-subject'].text = ev.content.email.subject
        fields['email-body'].text = ev.content.email.body
        if (ev.content.en) {
          hasEnglish.value = true
          enFields['sms-body'].text = ev.content.en.sms.body
          enFields['email-subject'].text = ev.content.en.email.subject
          enFields['email-body'].text = ev.content.en.email.body
        }
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
    versionStatus,
    versionId,
    worstCase,
    attempt,
    error,
  }
}
