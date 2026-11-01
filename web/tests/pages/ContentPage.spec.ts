// REQ-CNT-04, REQ-APR-01, REQ-APR-02, REQ-APR-03, REQ-UI-01 (UI): ContentPage composition
// (docs/contracts/ui.md §1.1, §3.23; DECISIONS D40, D45). REQ-ABT-04 C2–C8 (UI): the A/B toggle,
// variant tabs, angle, A/B errors in the invalid state, the draft's angles, preview variant and the
// variant route query.
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
  /** REQ-ABT-04 C6: GET /content-versions/{V2} (the stored invalid draft). */
  draftVersion?: ContentVersion
}

function api(s: Setup) {
  return fakeApi({
    [`GET ${C}`]: () => s.camp.value,
    [`GET ${C}/content-versions/${V2}`]: () =>
      s.draftVersion ?? version(V2, 2, content(), { status: 'invalid' }),
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

/** The preview follows the language query of the request (tr when absent). */
const followQuery = (r: RecordedRequest) =>
  langPreview(r.opts?.query?.language === 'en' ? 'en' : 'tr')

describe('ContentPage preview language (REQ-LNG-04 C4)', () => {
  it('the preview follows the selected language tab; there is no preview-language selector', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
      previewImpl: followQuery,
    }
    const { w, a } = await mountContent(setup)
    await flushPromises()
    expect(w.find(sel('preview-language')).exists()).toBe(false)
    expect(w.find(sel('preview-language-tr')).exists()).toBe(false)
    expect(w.find(sel('preview-language-en')).exists()).toBe(false)
    const first = a.last('GET', `${C}/preview`)
    expect(first?.opts?.query?.language).toBe('tr')
    expect(w.get(sel('sms-preview')).attributes('data-language')).toBe('tr')

    const before = a.count('GET', `${C}/preview`)
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThan(before)
    const next = a.last('GET', `${C}/preview`)
    expect(next?.opts?.query?.language).toBe('en')
    // sample_index is kept when the tab changes.
    expect(next?.opts?.query?.sample_index).toBe(first?.opts?.query?.sample_index)
    expect(w.get(sel('sms-preview')).attributes('data-language')).toBe('en')
    // The preview is split by channel: EmailPreview is shown on the email channel tab only.
    await w.get(sel('channel-tab-email')).trigger('click')
    await flushPromises()
    expect(w.get(sel('email-preview')).attributes('data-language')).toBe('en')
  })

  it('a Turkish-only version: no language-tabs and no language in the preview request', async () => {
    const { w, a } = await mountContent({
      camp: { value: campaign('content_ready', version()) },
      previewImpl: followQuery,
    })
    await flushPromises()
    expect(w.find(sel('language-tabs')).exists()).toBe(false)
    expect(w.find(sel('preview-language')).exists()).toBe(false)
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThanOrEqual(1)
    expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBeUndefined()
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

describe('ContentPage lang query (REQ-LNG-04 C6)', () => {
  it.each([
    { lang: 'en', withEN: true, selected: 'en' },
    { lang: 'tr', withEN: true, selected: 'tr' },
    { lang: 'xx', withEN: true, selected: 'tr' },
    { lang: 'en', withEN: false, selected: null },
  ])(
    'lang=$lang (version with en: $withEN) opens the $selected tab',
    async ({ lang, withEN, selected }) => {
      const { w, a } = await mountContent(
        {
          camp: {
            value: campaign('content_ready', withEN ? version(V1, 1, bilingual()) : version()),
          },
          previewImpl: followQuery,
        },
        { query: { lang } },
      )
      await flushPromises()
      if (selected === null) {
        expect(w.find(sel('language-tabs')).exists()).toBe(false)
        expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBeUndefined()
        return
      }
      expect(w.get(sel(`language-tab-${selected}`)).attributes('aria-selected')).toBe('true')
      expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBe(selected)
    },
  )

  it('changing the tab updates the lang query; content-to-approval carries it', async () => {
    const { w, router } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, bilingual())) },
      previewImpl: followQuery,
    })
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.lang).toBe('en')
    await w.get(sel('language-tab-tr')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.lang).toBe('tr')
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    await w.get(sel('content-to-approval')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('approval')
    expect(router.currentRoute.value.query.lang).toBe('en')
  })
})

// ---- A/B variants (P9) ----

const B_BLOCK = {
  sms: { body: 'B varyantı {{first_name}}' },
  email: { subject: 'B konu', body: 'B gövde {{first_name}}' },
}
const ANGLES = { a: 'İndirim oranı öne çıkar', b: 'Aciliyet: indirim bugün bitiyor' }

/** CNT_OK as variant A plus a variant B and both angles (REQ-ABT-01 C1 shape). */
function abContent(extra: Record<string, unknown> = {}) {
  return { ...content(), b: B_BLOCK, angles: ANGLES, ...extra } as never
}

/** A preview of the shown variant (REQ-ABT-03 C12: variant, customer.variant). */
function variantPreview(variant: 'a' | 'b') {
  const p = preview() as unknown as Record<string, unknown>
  return { ...p, variant, customer: { ...(p.customer as Record<string, unknown>), variant: 'b' } }
}

const followVariant = (r: RecordedRequest) =>
  variantPreview(r.opts?.query?.variant === 'b' ? 'b' : 'a')

/** A variant B delta (REQ-ABT-02 C7). */
const bDelta = (attempt: number, channel: string, field: string, text: string) =>
  frame('delta', { attempt, variant: 'b', channel, field, text })

/** The JSON body of the i-th generation request (fetch(url, init)). */
function generationBody(fetchMock: ReturnType<typeof vi.fn>, i = 0): unknown {
  const init = fetchMock.mock.calls[i]?.[1] as RequestInit | undefined
  return init?.body === undefined ? undefined : JSON.parse(String(init.body))
}

describe('ContentPage A/B toggle (REQ-ABT-04 C2)', () => {
  it('content-ab-toggle is a switch, off without b and on when the latest version has b', async () => {
    const off = await mountContent({ camp: { value: campaign('content_ready', version()) } })
    const t1 = off.w.get(sel('content-ab-toggle'))
    expect(t1.attributes('role')).toBe('switch')
    expect(t1.attributes('aria-checked')).toBe('false')
    off.w.unmount()
    wrapper = undefined
    document.body.innerHTML = ''

    const on = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
    })
    expect(on.w.get(sel('content-ab-toggle')).attributes('aria-checked')).toBe('true')
  })

  it('on: the generation body is {"ab":true}; off: {}', async () => {
    const { w, fetchMock } = await mountContent({
      camp: { value: campaign('audience_ready', null) },
    })
    await w.get(sel('content-ab-toggle')).trigger('click')
    await flushPromises()
    expect(w.get(sel('content-ab-toggle')).attributes('aria-checked')).toBe('true')
    await w.get(sel('content-regenerate')).trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(generationBody(fetchMock)).toEqual({ ab: true })
    w.unmount()
    wrapper = undefined
    document.body.innerHTML = ''

    const second = await mountContent({ camp: { value: campaign('audience_ready', null) } })
    await second.w.get(sel('content-regenerate')).trigger('click')
    await flushPromises()
    expect(generationBody(second.fetchMock)).toEqual({})
  })

  it('ab=1 in the route query switches the toggle on; generate and ab are removed from the query', async () => {
    const { w, router } = await mountContent(
      { camp: { value: campaign('audience_ready', null) } },
      { query: { generate: '1', ab: '1' } },
    )
    await flushPromises()
    expect(w.get(sel('content-ab-toggle')).attributes('aria-checked')).toBe('true')
    expect(router.currentRoute.value.query.ab).toBeUndefined()
    expect(router.currentRoute.value.query.generate).toBeUndefined()
  })

  it.each(['scheduled', 'sending', 'completed'])(
    'campaign %s disables the toggle',
    async (state) => {
      const { w } = await mountContent({ camp: { value: campaign(state, version()) } })
      expect(w.get(sel('content-ab-toggle')).attributes('aria-disabled')).toBe('true')
    },
  )

  it('is disabled while a generation streams (like content-regenerate)', async () => {
    const s = controllableStream()
    const fetchMock = vi.fn(async () => sseResponse(s.stream))
    const { w } = await mountContent(
      { camp: { value: campaign('audience_ready', null) } },
      { fetch: fetchMock },
    )
    await w.get(sel('content-regenerate')).trigger('click')
    s.push(delta(1, 'sms', 'body', 'Merhaba'))
    await vi.waitFor(() =>
      expect(w.get(sel('content-regenerate')).attributes('aria-disabled')).toBe('true'),
    )
    expect(w.get(sel('content-ab-toggle')).attributes('aria-disabled')).toBe('true')
    const before = w.get(sel('content-ab-toggle')).attributes('aria-checked')
    await w.get(sel('content-ab-toggle')).trigger('click')
    await flushPromises()
    expect(w.get(sel('content-ab-toggle')).attributes('aria-checked')).toBe(before)
  })
})

describe('ContentPage variant tabs (REQ-ABT-04 C3)', () => {
  it('a version with b shows content-variant-tabs; a is selected first; arrow keys move and select', async () => {
    const { w } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
    })
    expect(w.get(sel('content-variant-tabs')).attributes('role')).toBe('tablist')
    const a = () => w.get(sel('content-variant-tab-a'))
    const b = () => w.get(sel('content-variant-tab-b'))
    expect(a().attributes('role')).toBe('tab')
    expect(b().attributes('role')).toBe('tab')
    expect(a().attributes('aria-selected')).toBe('true')
    expect(b().attributes('aria-selected')).toBe('false')
    await a().trigger('keydown', { key: 'ArrowRight' })
    await flushPromises()
    expect(b().attributes('aria-selected')).toBe('true')
    expect(a().attributes('aria-selected')).toBe('false')
    await b().trigger('keydown', { key: 'ArrowLeft' })
    await flushPromises()
    expect(a().attributes('aria-selected')).toBe('true')
  })

  it('a version without b has no content-variant-tabs', async () => {
    const { w } = await mountContent({ camp: { value: campaign('content_ready', version()) } })
    expect(w.find(sel('content-variant-tabs')).exists()).toBe(false)
  })

  it('a variant b delta in the stream shows the tabs; the b tab streams the B text', async () => {
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
    expect(w.find(sel('content-variant-tabs')).exists()).toBe(false)
    s.push(bDelta(1, 'sms', 'body', 'Son gün'))
    await vi.waitFor(() => expect(w.find(sel('content-variant-tabs')).exists()).toBe(true))
    expect(w.find(sel('content-variant-angle')).exists()).toBe(false)
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(w.get(sel('streaming-text-content-sms-body')).element.textContent).toBe('Son gün')
  })

  it('the b tab shows the B texts in the editor', async () => {
    const { w } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
    })
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect((w.get(sel('editor-sms-body')).element as HTMLTextAreaElement).value).toBe(
      B_BLOCK.sms.body,
    )
  })

  it('b-prefixed errors mark the b tab and show inline only on the b tab; variants_identical in inline-error-variant', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [
          fieldError('b.sms.body', 'sms_too_long', 160, 161),
          fieldError('b', 'variants_identical'),
        ])
      },
    }
    const { w } = await mountContent(setup)
    await typeSMS(w, 'Düzenlenmiş SMS')
    await clickSave(w)
    expect(w.get(sel('content-variant-tab-b')).attributes('data-has-error')).toBe('true')
    expect(w.get(sel('content-variant-tab-a')).attributes('data-has-error')).not.toBe('true')
    expect(w.find(sel('inline-error-sms-body')).exists()).toBe(false)
    expect(w.find(sel('inline-error-variant')).exists()).toBe(false)
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
    expect(w.get(sel('inline-error-variant')).attributes('data-code')).toBe('variants_identical')
  })

  it('an unprefixed error marks the a tab and shows only while a is selected', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [fieldError('sms.body', 'sms_too_long', 160, 161)])
      },
    }
    const { w } = await mountContent(setup)
    await typeSMS(w, 'Düzenlenmiş SMS')
    await clickSave(w)
    expect(w.get(sel('content-variant-tab-a')).attributes('data-has-error')).toBe('true')
    expect(w.get(sel('content-variant-tab-b')).attributes('data-has-error')).not.toBe('true')
    expect(w.get(sel('inline-error-sms-body')).attributes('data-code')).toBe('sms_too_long')
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(w.find(sel('inline-error-sms-body')).exists()).toBe(false)
  })

  it('b.en.email.subject shows in inline-error-email-subject on the b tab with the en tab and email channel', async () => {
    const setup: Setup = {
      camp: {
        value: campaign(
          'content_ready',
          version(V1, 1, abContent({ en: EN, b: { ...B_BLOCK, en: EN } })),
        ),
      },
      previewImpl: followVariant,
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [
          fieldError('b.en.email.subject', 'subject_too_long', 80, 81),
        ])
      },
    }
    const { w } = await mountContent(setup)
    await typeSMS(w, 'Düzenlenmiş SMS')
    await clickSave(w)
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await w.get(sel('language-tab-en')).trigger('click')
    await w.get(sel('channel-tab-email')).trigger('click')
    await flushPromises()
    expect(w.get(sel('inline-error-email-subject')).attributes('data-code')).toBe(
      'subject_too_long',
    )
    await w.get(sel('content-variant-tab-a')).trigger('click')
    await flushPromises()
    expect(w.find(sel('inline-error-email-subject')).exists()).toBe(false)
  })
})

describe('ContentPage angle (REQ-ABT-04 C4)', () => {
  it('content-variant-angle and editor-angle show the selected variant angle', async () => {
    const { w } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
    })
    expect(w.get(sel('content-variant-angle')).element.textContent).toBe(ANGLES.a)
    expect((w.get(sel('editor-angle')).element as HTMLInputElement).value).toBe(ANGLES.a)
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(w.get(sel('content-variant-angle')).element.textContent).toBe(ANGLES.b)
    expect((w.get(sel('editor-angle')).element as HTMLInputElement).value).toBe(ANGLES.b)
  })

  it('an edited angle goes into the saved body', async () => {
    const { w, a } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
    })
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    await w.get(sel('editor-angle')).setValue('Yeni açı')
    await flushPromises()
    await clickSave(w)
    const body = a.last('POST', `${C}/content-versions`)?.opts?.body as Record<string, unknown>
    expect(body.angles).toEqual({ a: ANGLES.a, b: 'Yeni açı' })
    expect(body.b).toEqual(B_BLOCK)
  })

  it('angles errors show in inline-error-angle for the matching variant; the angle is text, not HTML', async () => {
    const setup: Setup = {
      camp: {
        value: campaign('content_ready', version(V1, 1, abContent({ angles: { a: P, b: 'B' } }))),
      },
      previewImpl: followVariant,
      saveImpl: () => {
        throw apiError(422, 'validation_failed', [fieldError('angles.a', 'too_long', 60, 61)])
      },
    }
    const { w } = await mountContent(setup)
    expect(w.get(sel('content-variant-angle')).element.textContent).toBe(P)
    expect(document.querySelector(`${sel('content-variant-angle')} script`)).toBeNull()
    await typeSMS(w, 'Düzenlenmiş SMS')
    await clickSave(w)
    expect(w.get(sel('inline-error-angle')).attributes('data-code')).toBe('too_long')
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(w.find(sel('inline-error-angle')).exists()).toBe(false)
  })
})

describe('ContentPage A/B errors in the invalid state (REQ-ABT-04 C5, C6)', () => {
  async function invalidRun(id: string | null, draft?: ContentVersion) {
    const s = controllableStream()
    const fetchMock = vi.fn(async () => sseResponse(s.stream))
    const r = await mountContent(
      {
        camp: { value: campaign('audience_ready', null) },
        draftVersion: draft,
        previewImpl: followVariant,
      },
      { fetch: fetchMock },
    )
    await r.w.get(sel('content-regenerate')).trigger('click')
    const errs = [fieldError('b', 'variants_identical'), fieldError('angles.a', 'required')]
    s.push(delta(1, 'sms', 'body', 'Merhaba'))
    s.push(bDelta(1, 'sms', 'body', 'Merhaba'))
    s.push(invalid(1, false, errs))
    s.push(delta(2, 'sms', 'body', 'Merhaba'))
    s.push(bDelta(2, 'sms', 'body', 'Merhaba'))
    s.push(invalid(2, true, errs, id))
    s.close()
    await vi.waitFor(() => expect(r.w.find(sel('content-edit-draft')).exists()).toBe(true))
    return r
  }

  it('page-level inline-error-angle (a tab) and inline-error-variant (b tab) after the final invalid', async () => {
    const { w } = await invalidRun(V2)
    expect(w.find(sel('editor-sms-body')).exists()).toBe(false)
    expect(w.get(sel('content-variant-tab-a')).attributes('aria-selected')).toBe('true')
    expect(w.get(sel('inline-error-angle')).attributes('data-code')).toBe('required')
    expect(w.find(sel('inline-error-variant')).exists()).toBe(false)
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(w.get(sel('inline-error-variant')).attributes('data-code')).toBe('variants_identical')
    expect(w.find(sel('inline-error-angle')).exists()).toBe(false)
  })

  it('content-edit-draft loads the angles of the stored draft and moves the errors into the editor', async () => {
    const draft = version(V2, 2, abContent({ angles: { a: '', b: 'Taslak açı' } }), {
      status: 'invalid',
    })
    const { w, a } = await invalidRun(V2, draft)
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    await w.get(sel('content-edit-draft')).trigger('click')
    await flushPromises()
    expect(a.count('GET', `${C}/content-versions/${V2}`)).toBeGreaterThanOrEqual(1)
    expect((w.get(sel('editor-angle')).element as HTMLInputElement).value).toBe('Taslak açı')
    expect(w.findAll(sel('inline-error-variant'))).toHaveLength(1)
    expect(w.get(sel('inline-error-variant')).attributes('data-code')).toBe('variants_identical')
  })

  it('a draft without a stored version starts with empty angles', async () => {
    const { w } = await invalidRun(null)
    await w.get(sel('content-edit-draft')).trigger('click')
    await flushPromises()
    expect((w.get(sel('editor-angle')).element as HTMLInputElement).value).toBe('')
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect((w.get(sel('editor-angle')).element as HTMLInputElement).value).toBe('')
  })
})

describe('ContentPage preview variant (REQ-ABT-04 C7)', () => {
  it('with variant tabs the preview asks for the selected variant and keeps sample_index', async () => {
    const { w, a } = await mountContent({
      camp: { value: campaign('content_ready', version(V1, 1, abContent())) },
      previewImpl: followVariant,
    })
    await flushPromises()
    const first = a.last('GET', `${C}/preview`)
    expect(first?.opts?.query?.variant).toBe('a')
    expect(w.get(sel('sms-preview')).attributes('data-variant')).toBe('a')
    const before = a.count('GET', `${C}/preview`)
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThan(before)
    const next = a.last('GET', `${C}/preview`)
    expect(next?.opts?.query?.variant).toBe('b')
    expect(next?.opts?.query?.sample_index).toBe(first?.opts?.query?.sample_index)
    expect(w.get(sel('sms-preview')).attributes('data-variant')).toBe('b')
    await w.get(sel('channel-tab-email')).trigger('click')
    await flushPromises()
    expect(w.get(sel('email-preview')).attributes('data-variant')).toBe('b')
  })

  it('with both tab rows the preview carries variant and language', async () => {
    const { w, a } = await mountContent({
      camp: {
        value: campaign(
          'content_ready',
          version(V1, 1, abContent({ en: EN, b: { ...B_BLOCK, en: EN } })),
        ),
      },
      previewImpl: followVariant,
    })
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    const q2 = a.last('GET', `${C}/preview`)?.opts?.query
    expect(q2?.variant).toBe('b')
    expect(q2?.language).toBe('en')
  })

  it('without variant tabs the preview request has no variant', async () => {
    const { a } = await mountContent({
      camp: { value: campaign('content_ready', version()) },
      previewImpl: followVariant,
    })
    await flushPromises()
    expect(a.last('GET', `${C}/preview`)?.opts?.query?.variant).toBeUndefined()
  })
})

describe('ContentPage variant query (REQ-ABT-04 C8)', () => {
  it.each([
    { variant: 'b', withB: true, selected: 'b' },
    { variant: 'a', withB: true, selected: 'a' },
    { variant: 'x', withB: true, selected: 'a' },
    { variant: 'b', withB: false, selected: null },
  ])(
    'variant=$variant (version with b: $withB) opens the $selected tab',
    async ({ variant, withB, selected }) => {
      const { w } = await mountContent(
        {
          camp: {
            value: campaign('content_ready', withB ? version(V1, 1, abContent()) : version()),
          },
          previewImpl: followVariant,
        },
        { query: { variant } },
      )
      await flushPromises()
      if (selected === null) {
        expect(w.find(sel('content-variant-tabs')).exists()).toBe(false)
        return
      }
      expect(w.get(sel(`content-variant-tab-${selected}`)).attributes('aria-selected')).toBe('true')
    },
  )

  it('changing the tab updates the variant query; content-to-approval carries variant and lang', async () => {
    const { w, router } = await mountContent({
      camp: {
        value: campaign(
          'content_ready',
          version(V1, 1, abContent({ en: EN, b: { ...B_BLOCK, en: EN } })),
        ),
      },
      previewImpl: followVariant,
    })
    await w.get(sel('content-variant-tab-b')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.variant).toBe('b')
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    await w.get(sel('content-to-approval')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('approval')
    expect(router.currentRoute.value.query.variant).toBe('b')
    expect(router.currentRoute.value.query.lang).toBe('en')
  })
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
