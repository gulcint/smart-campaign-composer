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
  frame,
  type RecordedRequest,
} from '../helpers/p4'
import { t } from '@/i18n/index'

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
  previewImpl?: (req: RecordedRequest) => unknown
}

function api(s: Setup) {
  return fakeApi({
    [`GET ${C}`]: () => s.camp.value,
    [`GET ${C}/preview`]: (r) => (s.previewImpl ? s.previewImpl(r) : preview()),
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
    // 150 template runes (D48 caps typing at 160); the server still reports the substituted length.
    await typeSMS(w, '{{first_name}}' + 'a'.repeat(136))
    await clickSave(w)
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
    expect(w.get(sel('editor-save')).attributes('aria-disabled')).not.toBe('true')
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      '{{first_name}}' + 'a'.repeat(136),
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
})

// ---- language (P8) ----

const EN = {
  sms: { body: 'Hi {{first_name}}, your cart is waiting.' },
  email: { subject: 'Your cart is waiting', body: 'Hi {{first_name}},\n\nYour cart is waiting.' },
}

/** CNT_OK with an English version (REQ-LNG-01 C1 shape). */
function bilingual() {
  return { ...content(), en: EN } as never
}

/** A preview of the given shown language (REQ-LNG-03 C6: language, fallback, customer.language). */
function langPreview(language: 'tr' | 'en', fallback = false, customerLanguage = language) {
  const p = preview() as unknown as Record<string, unknown>
  return {
    ...p,
    language,
    fallback,
    customer: { ...(p.customer as Record<string, unknown>), language: customerLanguage },
  }
}

describe('ContentPage language tabs (REQ-LNG-04 C3)', () => {
  it('a version with en shows language-tabs (role tablist) with the tr and en tabs', async () => {
    const { w } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
    })
    const tabs = w.get(sel('language-tabs'))
    expect(tabs.attributes('role')).toBe('tablist')
    expect(w.find(sel('language-tab-tr')).exists()).toBe(true)
    expect(w.find(sel('language-tab-en')).exists()).toBe(true)
  })

  it('a Turkish-only version shows no language-tabs', async () => {
    const { w } = await mountContent({ camp: { value: campaign('content_ready', version()) } })
    expect(w.find(sel('language-tabs')).exists()).toBe(false)
  })

  it('an English delta in the stream shows language-tabs', async () => {
    const s = controllableStream()
    const fetchMock = vi.fn(async () => sseResponse(s.stream))
    const { w } = await mountContent(
      { camp: { value: campaign('audience_ready', null) } },
      { fetch: fetchMock },
    )
    await w.get(sel('content-regenerate')).trigger('click')
    s.push(delta(1, 'sms', 'body', 'Merhaba'))
    await vi.waitFor(() =>
      expect(w.get(sel('streaming-text-content-sms-body')).element.textContent).toBe('Merhaba'),
    )
    expect(w.find(sel('language-tabs')).exists()).toBe(false)
    s.push(
      frame('delta', { attempt: 1, channel: 'sms', field: 'body', text: 'Hi', language: 'en' }),
    )
    await vi.waitFor(() => expect(w.find(sel('language-tabs')).exists()).toBe(true))
  })

  it('tabs are role=tab; tr is selected first; ArrowRight / ArrowLeft move and select', async () => {
    const { w } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
    })
    const tr = () => w.get(sel('language-tab-tr'))
    const en = () => w.get(sel('language-tab-en'))
    expect(tr().attributes('role')).toBe('tab')
    expect(en().attributes('role')).toBe('tab')
    expect(tr().attributes('aria-selected')).toBe('true')
    expect(en().attributes('aria-selected')).toBe('false')

    await tr().trigger('keydown', { key: 'ArrowRight' })
    await flushPromises()
    expect(en().attributes('aria-selected')).toBe('true')
    expect(tr().attributes('aria-selected')).toBe('false')

    await en().trigger('keydown', { key: 'ArrowLeft' })
    await flushPromises()
    expect(tr().attributes('aria-selected')).toBe('true')
    expect(en().attributes('aria-selected')).toBe('false')
  })

  it('en-prefixed field errors show in inline-error-<f> only while the en tab is selected', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [
          fieldError('en.sms.body', 'sms_too_long', 160, 161),
          fieldError('en.email.subject', 'subject_too_long', 80, 81),
          fieldError('en.email.body', 'required'),
        ])
      },
    }
    const { w } = await mountContent(setup)
    await typeSMS(w, 'Düzenlenmiş SMS')
    await clickSave(w)
    // tr tab selected: only unprefixed paths are shown, and there are none.
    for (const f of ['sms-body', 'email-subject', 'email-body']) {
      expect(w.find(sel(`inline-error-${f}`)).exists()).toBe(false)
    }
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    expect(w.get(sel('language-tab-en')).attributes('aria-selected')).toBe('true')
    // SMS channel selected: only the SMS field and its error; the email error shows on its tab.
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
    expect(w.find(sel('inline-error-email-subject')).exists()).toBe(false)
    expect(w.find(sel('inline-error-email-body')).exists()).toBe(false)
    expect(w.get(sel('channel-tab-email')).attributes('data-status')).toBe('invalid')

    // Email channel selected: the email errors inline; the SMS error shows on the SMS tab.
    await w.get(sel('channel-tab-email')).trigger('click')
    await flushPromises()
    expect(w.find(sel('inline-error-sms-body')).exists()).toBe(false)
    expect(w.get(sel('inline-error-email-subject')).attributes('data-code')).toBe(
      'subject_too_long',
    )
    expect(w.get(sel('inline-error-email-body')).attributes('data-code')).toBe('required')
    expect(w.get(sel('channel-tab-sms')).attributes('data-status')).toBe('invalid')
  })

  it('a Turkish error stays on the tr tab: tr shows it, en does not', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [fieldError('sms.body', 'sms_too_long', 160, 161)])
      },
    }
    const { w } = await mountContent(setup)
    await typeSMS(w, 'Düzenlenmiş SMS')
    await clickSave(w)
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
    expect(w.get(sel('language-tab-en')).attributes('data-has-error')).not.toBe('true')
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    expect(w.find(sel('inline-error-sms-body')).exists()).toBe(false)
  })

  it('a 422 on an en-prefixed path marks the en tab with data-has-error, not the tr tab', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [
          fieldError('en.sms.body', 'sms_too_long', 160, 161),
        ])
      },
    }
    const { w } = await mountContent(setup)
    await typeSMS(w, 'Düzenlenmiş SMS')
    await clickSave(w)
    expect(w.get(sel('language-tab-en')).attributes('data-has-error')).toBe('true')
    expect(w.get(sel('language-tab-tr')).attributes('data-has-error')).not.toBe('true')
  })
})

describe('ContentPage preview language (REQ-LNG-04 C4)', () => {
  it('preview-language marks the shown language with aria-pressed; choosing en re-requests the preview with language=en', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
      previewImpl: (r) => langPreview(r.opts?.query?.language === 'en' ? 'en' : 'tr'),
    }
    const { w, a } = await mountContent(setup)
    await flushPromises()
    expect(w.find(sel('preview-language')).exists()).toBe(true)
    expect(w.get(sel('preview-language-tr')).attributes('aria-pressed')).toBe('true')
    expect(w.get(sel('preview-language-en')).attributes('aria-pressed')).not.toBe('true')

    const before = a.count('GET', `${C}/preview`)
    await w.get(sel('preview-language-en')).trigger('click')
    await flushPromises()
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThan(before)
    expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBe('en')
    expect(w.get(sel('preview-language-en')).attributes('aria-pressed')).toBe('true')
    expect(w.get(sel('preview-language-tr')).attributes('aria-pressed')).not.toBe('true')
  })

  it.each([
    { fallback: true, shown: true },
    { fallback: false, shown: false },
  ])(
    'preview.fallback=$fallback => preview-fallback-note shown=$shown',
    async ({ fallback, shown }) => {
      const { w } = await mountContent({
        camp: { value: campaign('content_ready', version()) },
        previewImpl: () => langPreview('tr', fallback, 'en'),
      })
      await flushPromises()
      const note = w.find(sel('preview-fallback-note'))
      expect(note.exists()).toBe(shown)
      if (shown) expect(note.text()).toBe(t('preview.fallbackNote'))
    },
  )
})

describe('ContentPage locked states (REQ-APR-03)', () => {
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
})
