// REQ-CNT-04, REQ-AI-01 (UI): useContentGeneration state machine over a fetch + ReadableStream SSE
// body (docs/contracts/ui.md §5.2; sse-events.md §1.2, §1.3.1).
import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { useContentGeneration } from '@/composables/useContentGeneration'
import { FetchKey } from '@/api/client'
import {
  CAMPAIGN_ID,
  V1,
  V2,
  content,
  controllableStream,
  delta,
  errorBody,
  fieldError,
  invalid,
  jsonResponse,
  sseResponse,
  validated,
} from '../helpers/p4'

type Gen = ReturnType<typeof useContentGeneration>
const FIELDS = ['sms-body', 'email-subject', 'email-body'] as const

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

function setup(fetchImpl: (url: string, init?: RequestInit) => Promise<Response>) {
  const fetchMock = vi.fn(fetchImpl)
  let gen!: Gen
  const Host = defineComponent({
    setup() {
      gen = useContentGeneration()
      return () => h('div')
    },
  })
  wrapper = mount(Host, { global: { provide: { [FetchKey as symbol]: fetchMock } } })
  return { gen, fetchMock }
}

function streaming() {
  const s = controllableStream()
  const { gen, fetchMock } = setup(async () => sseResponse(s.stream))
  return { gen, fetchMock, s }
}

function phases(gen: Gen) {
  return FIELDS.map((f) => gen.fields[f].phase)
}

describe('useContentGeneration start (REQ-CNT-04)', () => {
  it('start resets every field to streaming and POSTs to the content endpoint', async () => {
    const { gen, fetchMock, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    expect(phases(gen)).toEqual(['streaming', 'streaming', 'streaming'])
    expect(FIELDS.map((f) => gen.fields[f].text)).toEqual(['', '', ''])
    expect(gen.versionStatus.value).toBe('streaming')
    expect(gen.attempt.value).toBe(1)
    expect(gen.error.value).toBeNull()
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const [url, init] = fetchMock.mock.calls[0] ?? []
    expect(String(url)).toMatch(new RegExp(`/api/campaigns/${CAMPAIGN_ID}/content$`))
    expect(init?.method).toBe('POST')
    const hdrs = new Headers(init?.headers)
    expect(hdrs.get('Accept')).toBe('text/event-stream')
    expect(init?.body).toBe('{}')
    expect(init?.signal).toBeDefined()
    s.push(validated(content()))
    s.close()
    await done
  })
})

describe('useContentGeneration events (REQ-CNT-04)', () => {
  it('deltas accumulate per field; validated rewrites the texts from its content', async () => {
    const { gen, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    s.push(delta(1, 'sms', 'body', 'Merhaba {{first_'))
    s.push(delta(1, 'sms', 'body', 'name}}'))
    s.push(delta(1, 'email', 'subject', 'Konu'))
    await vi.waitFor(() => expect(gen.fields['sms-body'].text).toBe('Merhaba {{first_name}}'))
    expect(gen.fields['email-subject'].text).toBe('Konu')
    expect(gen.fields['sms-body'].phase).toBe('streaming')
    const authoritative = content('Otorite SMS', 'Otorite konu', 'Otorite gövde')
    s.push(validated(authoritative, V1, 1, 1, { sms_runes: 11, subject_runes: 12 }))
    s.close()
    await done
    expect(gen.versionStatus.value).toBe('validated')
    expect(phases(gen)).toEqual(['validated', 'validated', 'validated'])
    expect(gen.fields['sms-body'].text).toBe('Otorite SMS')
    expect(gen.fields['email-subject'].text).toBe('Otorite konu')
    expect(gen.fields['email-body'].text).toBe('Otorite gövde')
    expect(gen.versionId.value).toBe(V1)
    expect(gen.worstCase.value).toEqual({ sms_runes: 11, subject_runes: 12 })
  })

  it('frames split mid-line and heartbeat comments are handled', async () => {
    const { gen, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    const f = delta(1, 'sms', 'body', 'Merhaba')
    s.push(': keepalive\n\n')
    s.push(f.slice(0, 10))
    s.push(f.slice(10, 25))
    s.push(f.slice(25))
    await vi.waitFor(() => expect(gen.fields['sms-body'].text).toBe('Merhaba'))
    s.push(validated(content()))
    s.close()
    await done
    expect(gen.versionStatus.value).toBe('validated')
  })

  it('a non-final invalid clears every text and marks retrying; attempt-2 deltas restart', async () => {
    const { gen, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    s.push(delta(1, 'sms', 'body', 'İndirim İndirim'))
    s.push(delta(1, 'email', 'subject', 'Konu'))
    s.push(invalid(1, false, [fieldError('sms.body', 'sms_too_long', 160, 161)]))
    await vi.waitFor(() => expect(gen.fields['sms-body'].phase).toBe('retrying'))
    expect(phases(gen)).toEqual(['retrying', 'retrying', 'retrying'])
    expect(FIELDS.map((f) => gen.fields[f].text)).toEqual(['', '', ''])
    expect(FIELDS.map((f) => gen.fields[f].errors.length)).toEqual([0, 0, 0])
    s.push(delta(2, 'sms', 'body', 'Merhaba'))
    await vi.waitFor(() => expect(gen.fields['sms-body'].phase).toBe('streaming'))
    expect(gen.fields['sms-body'].text).toBe('Merhaba')
    expect(gen.attempt.value).toBe(2)
    s.push(validated(content(), V1, 1, 2))
    s.close()
    await done
    expect(gen.versionStatus.value).toBe('validated')
  })

  it('a final invalid keeps the texts, routes errors by path ("" to every field) and keeps the draft id', async () => {
    const { gen, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    s.push(invalid(1, false, [fieldError('', 'invalid_json')]))
    s.push(delta(2, 'sms', 'body', 'İndirim'))
    s.push(
      invalid(
        2,
        true,
        [fieldError('sms.body', 'sms_too_long', 160, 161), fieldError('', 'internal_error')],
        V2,
      ),
    )
    s.close()
    await done
    expect(gen.versionStatus.value).toBe('invalid')
    expect(phases(gen)).toEqual(['invalid', 'invalid', 'invalid'])
    expect(gen.fields['sms-body'].text).toBe('İndirim')
    expect(gen.fields['sms-body'].errors.map((e) => e.code).sort()).toEqual([
      'internal_error',
      'sms_too_long',
    ])
    expect(gen.fields['email-subject'].errors.map((e) => e.code)).toEqual(['internal_error'])
    expect(gen.fields['email-body'].errors.map((e) => e.code)).toEqual(['internal_error'])
    expect(gen.versionId.value).toBe(V2)
  })

  it('a final invalid without a stored draft leaves versionId null', async () => {
    const { gen, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    s.push(invalid(1, true, [fieldError('', 'llm_unavailable')], null))
    s.close()
    await done
    expect(gen.versionStatus.value).toBe('invalid')
    expect(gen.versionId.value).toBeNull()
  })
})

describe('useContentGeneration interruptions (REQ-CNT-04, sse-events.md §1.3.1)', () => {
  it('a body that ends without a terminal event is interrupted (no reconnect)', async () => {
    const { gen, fetchMock, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    s.push(delta(1, 'sms', 'body', 'Merh'))
    s.close()
    await done
    expect(gen.versionStatus.value).toBe('interrupted')
    expect(phases(gen)).toEqual(['interrupted', 'interrupted', 'interrupted'])
    expect(gen.fields['sms-body'].text).toBe('Merh')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('a network error after 200 is interrupted, not an ApiError', async () => {
    const { gen, s } = streaming()
    const done = gen.start(CAMPAIGN_ID)
    s.push(delta(1, 'sms', 'body', 'Merh'))
    await vi.waitFor(() => expect(gen.fields['sms-body'].text).toBe('Merh'))
    s.error()
    await done
    expect(gen.versionStatus.value).toBe('interrupted')
    expect(gen.error.value).toBeNull()
  })
})

describe('useContentGeneration errors before the stream (REQ-CNT-04, ui.md §4)', () => {
  it.each([
    { status: 409, code: 'generation_in_progress' },
    { status: 409, code: 'invalid_state' },
    { status: 502, code: 'llm_unavailable' },
    { status: 404, code: 'campaign_not_found' },
  ])(
    '$status $code becomes error; fields idle, versionStatus none, start resolves',
    async ({ status, code }) => {
      const { gen } = setup(async () => jsonResponse(status, errorBody(code)))
      await expect(gen.start(CAMPAIGN_ID)).resolves.toBeUndefined()
      expect(gen.error.value?.status).toBe(status)
      expect(gen.error.value?.code).toBe(code)
      expect(gen.versionStatus.value).toBe('none')
      expect(phases(gen)).toEqual(['idle', 'idle', 'idle'])
    },
  )
})
