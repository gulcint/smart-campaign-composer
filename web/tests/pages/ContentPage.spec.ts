// REQ-CNT-04, REQ-APR-01, REQ-APR-02, REQ-APR-03, REQ-UI-01 (UI): ContentPage composition
// (docs/contracts/ui.md §1.1, §3.23; DECISIONS D40, D45).
import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import ContentPage from '@/pages/ContentPage.vue'
import type { Campaign, ContentVersion } from '@/api/types'
import { mountPage } from '../helpers/pages'
import {
  CAMPAIGN_ID,
  P,
  V1,
  V2,
  apiError,
  campaign,
  content,
  controllableStream,
  delta,
  fieldError,
  header,
  invalid,
  preview,
  sel,
  sseResponse,
  validated,
  version,
  fakeApi,
} from '../helpers/p4'

const C = `/campaigns/${CAMPAIGN_ID}`

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  document.body.innerHTML = ''
})

interface Setup {
  camp: { value: Campaign }
  saveImpl?: (body: unknown) => unknown
}

function api(s: Setup) {
  return fakeApi({
    [`GET ${C}`]: () => s.camp.value,
    [`GET ${C}/preview`]: () => preview(),
    [`POST ${C}/content-versions`]: (r) => {
      if (s.saveImpl) return s.saveImpl(r.opts?.body)
      return version(V2, 2, r.opts?.body as never, { source: 'edit', llm_attempts: null })
    },
  })
}

async function mountContent(
  s: Setup,
  opts: { fetch?: unknown; query?: Record<string, string> } = {},
) {
  const a = api(s)
  const fetchMock = opts.fetch ?? vi.fn(async () => sseResponse(controllableStream().stream))
  const r = await mountPage(ContentPage, 'content', {
    api: a.client,
    fetch: fetchMock,
    query: opts.query,
  })
  wrapper = r.wrapper
  return { a, fetchMock: fetchMock as ReturnType<typeof vi.fn>, router: r.router, w: r.wrapper }
}

const q = (id: string) => document.querySelector(sel(id)) as HTMLElement | null

async function typeSMS(w: VueWrapper, text: string) {
  await w.get(sel('editor-sms-body')).setValue(text)
  await flushPromises()
}

async function clickSave(w: VueWrapper) {
  await w.get(sel('editor-save')).trigger('click')
  await flushPromises()
}

describe('ContentPage on mount (REQ-CNT-04, ui.md §1.1)', () => {
  it('with a latest version shows it in the editor, requests a preview and never starts a generation', async () => {
    const { w, fetchMock, a } = await mountContent({
      camp: { value: campaign('content_ready', version()) },
    })
    expect(w.find(sel('page-content')).exists()).toBe(true)
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      content().sms.body,
    )
    expect(fetchMock).not.toHaveBeenCalled()
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThanOrEqual(1)
    expect(w.get(sel('content-to-approval')).attributes('aria-disabled')).not.toBe('true')
    expect(w.find(sel('content-live-status')).attributes('role')).toBe('status')
  })

  it('without a version shows content-empty; content-regenerate starts a generation without a dialog', async () => {
    const { w, fetchMock } = await mountContent({
      camp: { value: campaign('audience_ready', null) },
    })
    expect(w.find(sel('content-empty')).exists()).toBe(true)
    expect(fetchMock).not.toHaveBeenCalled()
    await w.get(sel('content-regenerate')).trigger('click')
    await flushPromises()
    expect(q('confirm-dialog')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(String(fetchMock.mock.calls[0]?.[0])).toMatch(
      new RegExp(`/api/campaigns/${CAMPAIGN_ID}/content$`),
    )
  })

  it('?generate=1 starts exactly one generation and removes the query', async () => {
    const { fetchMock, router } = await mountContent(
      { camp: { value: campaign('audience_ready', null) } },
      { query: { generate: '1' } },
    )
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    await flushPromises()
    expect(router.currentRoute.value.query.generate).toBeUndefined()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('renders stored content as text, never HTML (REQ-UI-01)', async () => {
    const { w } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, content(P))) },
    })
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(P)
    expect(document.querySelector(`${sel('page-content')} script`)).toBeNull()
    expect(document.querySelector(`${sel('page-content')} b`)).toBeNull()
  })
})

describe('ContentPage streaming (REQ-CNT-04)', () => {
  it('while streaming: StreamingText, no editor, regenerate disabled; validated shows the editor and requests a preview', async () => {
    const s = controllableStream()
    const fetchMock = vi.fn(async () => sseResponse(s.stream))
    const setup = { camp: { value: campaign('audience_ready', null) } }
    const { w, a } = await mountContent(setup, { fetch: fetchMock })
    await w.get(sel('content-regenerate')).trigger('click')
    s.push(delta(1, 'sms', 'body', 'Merhaba'))
    // start() renders the three StreamingText at once (ui.md §5.2, §3.23): wait for the text itself.
    await vi.waitFor(() =>
      expect(w.get(sel('streaming-text-content-sms-body')).element.textContent).toBe('Merhaba'),
    )
    expect(w.find(sel('editor-sms-body')).exists()).toBe(false)
    expect(w.get(sel('content-regenerate')).attributes('aria-disabled')).toBe('true')

    const before = a.count('GET', `${C}/preview`)
    setup.camp.value = campaign('content_ready', version(V1, 1))
    s.push(validated(content(), V1, 1))
    s.close()
    await vi.waitFor(() => expect(w.find(sel('editor-sms-body')).exists()).toBe(true))
    await flushPromises()
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      content().sms.body,
    )
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThan(before)
  })

  it('a final invalid offers content-edit-draft; saving the draft is based on "none" (drafts are not a base)', async () => {
    const s = controllableStream()
    const fetchMock = vi.fn(async () => sseResponse(s.stream))
    const { w, a } = await mountContent(
      { camp: { value: campaign('audience_ready', null) } },
      { fetch: fetchMock },
    )
    await w.get(sel('content-regenerate')).trigger('click')
    s.push(invalid(1, false, [fieldError('sms.body', 'sms_too_long', 160, 161)]))
    s.push(delta(2, 'sms', 'body', 'Uzun taslak'))
    s.push(invalid(2, true, [fieldError('sms.body', 'sms_too_long', 160, 161)], V2))
    s.close()
    await vi.waitFor(() => expect(w.find(sel('content-edit-draft')).exists()).toBe(true))
    expect(w.get(sel('streaming-text-sms-body')).attributes('data-phase')).toBe('invalid')

    await w.get(sel('content-edit-draft')).trigger('click')
    await flushPromises()
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe('Uzun taslak')
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
    expect(w.find(sel('streaming-text-sms-body')).exists()).toBe(false)
    await typeSMS(w, 'Kısa taslak')
    await clickSave(w)
    expect(header(a.last('POST', `${C}/content-versions`), 'X-Expected-Version')).toBe('none')
  })
})

describe('ContentPage save (REQ-APR-01, REQ-APR-02, REQ-APR-03)', () => {
  it('sends the full content with X-Expected-Version = latest id; the next save uses the new id', async () => {
    const setup: Setup = { camp: { value: campaign('content_ready', version(V1, 1)) } }
    const { w, a } = await mountContent(setup)
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).toBe('true')
    await typeSMS(w, 'Düzenlenmiş SMS')
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).not.toBe('true')
    setup.camp.value = campaign(
      'content_ready',
      version(V2, 2, content('Düzenlenmiş SMS'), { source: 'edit' }),
    )
    await clickSave(w)
    const first = a.last('POST', `${C}/content-versions`)
    expect(header(first, 'X-Expected-Version')).toBe(V1)
    expect(first?.opts?.body).toEqual(content('Düzenlenmiş SMS'))
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).toBe('true')

    const v3 = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
    setup.saveImpl = (b) => version(v3, 3, b as never, { source: 'edit' })
    await typeSMS(w, 'İkinci düzenleme')
    await clickSave(w)
    expect(header(a.last('POST', `${C}/content-versions`), 'X-Expected-Version')).toBe(V2)
  })

  it('422 puts the field errors inline and keeps the editor dirty', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version()) },
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [fieldError('sms.body', 'sms_too_long', 160, 161)])
      },
    }
    const { w } = await mountContent(setup)
    await typeSMS(w, 'a'.repeat(161))
    await clickSave(w)
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).not.toBe('true')
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      'a'.repeat(161),
    )
  })

  it('409 generation_in_progress keeps the edit, refetches the campaign and shows no conflict panel', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version()) },
      saveImpl: () => {
        throw apiError(409, 'generation_in_progress')
      },
    }
    const { w, a } = await mountContent(setup)
    const gets = a.count('GET', C)
    await typeSMS(w, 'Benim metnim')
    await clickSave(w)
    expect(a.count('GET', C)).toBeGreaterThan(gets)
    expect(w.find(sel('editor-conflict')).exists()).toBe(false)
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      'Benim metnim',
    )
  })
})

describe('ContentPage edit conflict (REQ-APR-03, D40)', () => {
  function conflictSetup(latestAfter: ContentVersion): Setup {
    const setup: Setup = { camp: { value: campaign('content_ready', version(V1, 1)) } }
    setup.saveImpl = () => {
      setup.camp.value = campaign('content_ready', latestAfter)
      throw apiError(409, 'content_edit_conflict', [], {
        id: latestAfter.id,
        version_number: latestAfter.version_number,
      })
    }
    return setup
  }

  it('shows editor-conflict with the current version, keeps the user text and warns about keep-mine', async () => {
    const setup = conflictSetup(version(V2, 2, content('Başkasının metni')))
    const { w, a } = await mountContent(setup)
    const gets = a.count('GET', C)
    await typeSMS(w, 'Benim metnim')
    await clickSave(w)
    expect(a.count('GET', C)).toBeGreaterThan(gets)
    const panel = w.get(sel('editor-conflict'))
    expect(panel.attributes('data-current-version-id')).toBe(V2)
    expect(panel.attributes('data-value')).toBe('2')
    expect(panel.find(sel('editor-conflict-warning')).exists()).toBe(true)
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      'Benim metnim',
    )
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).toBe('true')
  })

  it('reload-latest replaces the text with the latest version and closes the conflict', async () => {
    const setup = conflictSetup(version(V2, 2, content('Başkasının metni')))
    const { w, a } = await mountContent(setup)
    await typeSMS(w, 'Benim metnim')
    await clickSave(w)
    const previews = a.count('GET', `${C}/preview`)
    await w.get(sel('editor-conflict-reload')).trigger('click')
    await flushPromises()
    expect(w.find(sel('editor-conflict')).exists()).toBe(false)
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      'Başkasının metni',
    )
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).toBe('true')
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThan(previews)
  })

  it('keep-mine keeps the text, rebases onto the latest version and the next save uses its id', async () => {
    const setup = conflictSetup(version(V2, 2, content('Başkasının metni')))
    const { w, a } = await mountContent(setup)
    await typeSMS(w, 'Benim metnim')
    await clickSave(w)
    await w.get(sel('editor-conflict-keep-mine')).trigger('click')
    await flushPromises()
    expect(w.find(sel('editor-conflict')).exists()).toBe(false)
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      'Benim metnim',
    )
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).not.toBe('true')
    expect(q('confirm-dialog')).toBeNull()

    setup.saveImpl = (b) =>
      version('aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee', 3, b as never, { source: 'edit' })
    await clickSave(w)
    const last = a.last('POST', `${C}/content-versions`)
    expect(header(last, 'X-Expected-Version')).toBe(V2)
    expect((last?.opts?.body as ReturnType<typeof content>).sms.body).toBe('Benim metnim')
  })

  it('a conflict whose latest content equals the editor text is treated as success (retried request)', async () => {
    const setup = conflictSetup(version(V2, 2, content('Benim metnim')))
    const { w } = await mountContent(setup)
    await typeSMS(w, 'Benim metnim')
    await clickSave(w)
    expect(w.find(sel('editor-conflict')).exists()).toBe(false)
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).toBe('true')
  })
})

describe('ContentPage regenerate and locked states (REQ-CNT-04, REQ-APR-01)', () => {
  it('regenerate with a validated version asks for confirmation first', async () => {
    const { w, fetchMock } = await mountContent({
      camp: { value: campaign('content_ready', version()) },
    })
    await w.get(sel('content-regenerate')).trigger('click')
    await flushPromises()
    expect(fetchMock).not.toHaveBeenCalled()
    await vi.waitFor(() =>
      expect(q('confirm-dialog')?.getAttribute('data-kind')).toBe('regenerate'),
    )
    q('confirm-dialog-confirm')?.click()
    await flushPromises()
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
  })

  it.each(['sending', 'completed'])(
    'campaign %s locks the editor and the actions',
    async (state) => {
      const { w } = await mountContent({ camp: { value: campaign(state, version()) } })
      expect(w.find(sel('editor-locked-note')).exists()).toBe(true)
      expect(w.get(sel('editor-save')).attributes('aria-disabled')).toBe('true')
      expect(w.get(sel('content-regenerate')).attributes('aria-disabled')).toBe('true')
      expect(w.get(sel('editor-sms-body')).attributes('readonly')).toBeDefined()
    },
  )

  it('content-to-approval is disabled while the editor is dirty', async () => {
    const { w } = await mountContent({ camp: { value: campaign('content_ready', version()) } })
    await typeSMS(w, 'Kaydedilmemiş')
    expect(w.get(sel('content-to-approval')).attributes('aria-disabled')).toBe('true')
  })
})
