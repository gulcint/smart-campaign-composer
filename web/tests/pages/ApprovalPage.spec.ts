// REQ-APR-01, REQ-APR-03, REQ-CNT-05 (UI): ApprovalPage composition (docs/contracts/ui.md §3.24,
// §3.17, §3.18, §4). REQ-SCH-04 C4, C6 (UI): planning a send, the scheduled state and its cancel.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import ApprovalPage from '@/pages/ApprovalPage.vue'
import type { Campaign } from '@/api/types'
import { t } from '@/i18n/index'
import { formatTsi } from '@/lib/schedule'
import { mountPage } from '../helpers/pages'
import {
  CAMPAIGN_ID,
  T0,
  V1,
  V3,
  apiError,
  campaign,
  fakeApi,
  fieldError,
  preview,
  sel,
  version,
  type RecordedRequest,
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
  approve?: () => unknown
  send?: () => unknown
  cancelSchedule?: () => unknown
  previewImpl?: (req: RecordedRequest) => unknown
  query?: Record<string, string>
}

async function mountApproval(s: Setup) {
  const a = fakeApi({
    [`GET ${C}`]: () => s.camp.value,
    [`DELETE ${C}/schedule`]: () =>
      s.cancelSchedule
        ? s.cancelSchedule()
        : { campaign_id: CAMPAIGN_ID, state: 'approved', scheduled_at: null },
    [`GET ${C}/preview`]: (r) => (s.previewImpl ? s.previewImpl(r) : preview()),
    [`POST ${C}/approve`]: () =>
      s.approve ? s.approve() : campaign('approved', s.camp.value.latest_content_version),
    [`POST ${C}/send`]: () =>
      s.send
        ? s.send()
        : {
            campaign_id: CAMPAIGN_ID,
            state: 'sending',
            content_version_id: V1,
            deliveries: { total: 718, sms: 350, email: 368 },
          },
  })
  const r = await mountPage(ApprovalPage, 'approval', {
    api: a.client,
    withToaster: true,
    query: s.query,
  })
  wrapper = r.wrapper
  return { a, router: r.router, w: r.wrapper }
}

const q = (id: string) => document.querySelector(sel(id)) as HTMLElement | null

async function openAndConfirm(w: VueWrapper, button: string) {
  await w.get(sel(button)).trigger('click')
  await flushPromises()
  await vi.waitFor(() => expect(q('confirm-dialog')).not.toBeNull())
  q('confirm-dialog-confirm')?.click()
  await flushPromises()
}

// ---- language (P8) ----

const EN_BLOCK = {
  sms: { body: 'Hi {{first_name}}' },
  email: { subject: 'Your cart', body: 'Hi {{first_name}}' },
}

/** A bilingual version whose worst case carries the English measurement enWC. */
function bilingualVersion(enWC = { sms_runes: 120, subject_runes: 20 }) {
  return version(
    V1,
    1,
    { ...version().content, en: EN_BLOCK } as never,
    {
      worst_case: { sms_runes: 160, subject_runes: 42, en: enWC },
    } as never,
  )
}

/** A preview of the shown language (REQ-LNG-03 C6 fields). */
function langPreview(language: 'tr' | 'en', fallback = false, customerLanguage: string = language) {
  const p = preview() as unknown as Record<string, unknown>
  return {
    ...p,
    language,
    fallback,
    customer: { ...(p.customer as Record<string, unknown>), language: customerLanguage },
  }
}

/** The preview follows the language query of the request (tr when absent). */
const followQuery = (r: RecordedRequest) =>
  langPreview(r.opts?.query?.language === 'en' ? 'en' : 'tr')

describe('ApprovalPage preview language (REQ-LNG-04 C4)', () => {
  it('has no preview-language group on any version', async () => {
    const { w } = await mountApproval({
      camp: { value: campaign('content_ready', bilingualVersion()) },
      previewImpl: followQuery,
    })
    expect(w.find(sel('page-approval')).exists()).toBe(true)
    expect(w.find(sel('preview-language')).exists()).toBe(false)
    expect(w.find(sel('preview-language-tr')).exists()).toBe(false)
    expect(w.find(sel('preview-language-en')).exists()).toBe(false)
  })

  it.each([
    { fallback: true, shown: true },
    { fallback: false, shown: false },
  ])(
    'preview.fallback=$fallback => preview-fallback-note shown=$shown',
    async ({ fallback, shown }) => {
      const { w } = await mountApproval({
        camp: { value: campaign('content_ready', version()) },
        previewImpl: () => langPreview('tr', fallback, 'en'),
      })
      const note = w.find(sel('preview-fallback-note'))
      expect(note.exists()).toBe(shown)
      if (shown) expect(note.text()).toBe(t('preview.fallbackNote'))
    },
  )

  it('a Turkish-only version: no language-tabs and no language in the preview request', async () => {
    const { w, a } = await mountApproval({
      camp: { value: campaign('content_ready', version()) },
      previewImpl: followQuery,
    })
    expect(w.find(sel('language-tabs')).exists()).toBe(false)
    expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBeUndefined()
  })
})

describe('ApprovalPage language tabs (REQ-LNG-04 C5)', () => {
  it('a version with en shows language-tabs; tr is selected first; arrow keys move and select', async () => {
    const { w } = await mountApproval({
      camp: { value: campaign('content_ready', bilingualVersion()) },
      previewImpl: followQuery,
    })
    expect(w.get(sel('language-tabs')).attributes('role')).toBe('tablist')
    const tr = () => w.get(sel('language-tab-tr'))
    const en = () => w.get(sel('language-tab-en'))
    expect(tr().attributes('role')).toBe('tab')
    expect(en().attributes('role')).toBe('tab')
    expect(tr().attributes('aria-selected')).toBe('true')
    expect(en().attributes('aria-selected')).toBe('false')
    await tr().trigger('keydown', { key: 'ArrowRight' })
    await flushPromises()
    expect(en().attributes('aria-selected')).toBe('true')
    await en().trigger('keydown', { key: 'ArrowLeft' })
    await flushPromises()
    expect(tr().attributes('aria-selected')).toBe('true')
  })

  it('the selected language drives approval-preview data-language and the limit check values', async () => {
    const { w, a } = await mountApproval({
      camp: { value: campaign('content_ready', bilingualVersion()) },
      previewImpl: followQuery,
    })
    expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBe('tr')
    expect(w.get(sel('approval-preview')).attributes('data-language')).toBe('tr')
    const check = () => w.get(sel('approval-limit-check'))
    expect(check().attributes('data-sms')).toBe('160')
    expect(check().attributes('data-subject')).toBe('42')
    expect(check().attributes('data-state')).toBe('pass')

    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBe('en')
    expect(w.get(sel('approval-preview')).attributes('data-language')).toBe('en')
    expect(check().attributes('data-sms')).toBe('120')
    expect(check().attributes('data-subject')).toBe('20')
    expect(check().attributes('data-state')).toBe('pass')
  })

  it('the limit check does not pass when the English version is over a limit, even on the tr tab', async () => {
    const { w } = await mountApproval({
      camp: {
        value: campaign('content_ready', bilingualVersion({ sms_runes: 161, subject_runes: 20 })),
      },
      previewImpl: followQuery,
    })
    expect(w.get(sel('language-tab-tr')).attributes('aria-selected')).toBe('true')
    expect(w.get(sel('approval-limit-check')).attributes('data-state')).not.toBe('pass')
  })
})

describe('ApprovalPage lang query (REQ-LNG-04 C6)', () => {
  it.each([
    { lang: 'en', withEN: true, selected: 'en' },
    { lang: 'tr', withEN: true, selected: 'tr' },
    { lang: 'xx', withEN: true, selected: 'tr' },
    { lang: 'en', withEN: false, selected: null },
  ])(
    'lang=$lang (version with en: $withEN) opens the $selected tab',
    async ({ lang, withEN, selected }) => {
      const { w, a } = await mountApproval({
        camp: { value: campaign('content_ready', withEN ? bilingualVersion() : version()) },
        previewImpl: followQuery,
        query: { lang },
      })
      if (selected === null) {
        expect(w.find(sel('language-tabs')).exists()).toBe(false)
        expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBeUndefined()
        return
      }
      expect(w.get(sel(`language-tab-${selected}`)).attributes('aria-selected')).toBe('true')
      expect(a.last('GET', `${C}/preview`)?.opts?.query?.language).toBe(selected)
    },
  )

  it('changing the tab updates the lang query; approval-edit carries it to the content step', async () => {
    const { w, router } = await mountApproval({
      camp: { value: campaign('content_ready', bilingualVersion()) },
      previewImpl: followQuery,
    })
    await w.get(sel('language-tab-en')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.lang).toBe('en')
    await w.get(sel('approval-edit')).trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('content')
    expect(router.currentRoute.value.query.lang).toBe('en')
  })
})

describe('ApprovalPage (REQ-APR-01)', () => {
  it('shows the latest version, the channel audiences and requests a preview', async () => {
    const { w, a } = await mountApproval({
      camp: { value: campaign('content_ready', version(V1, 3)) },
    })
    expect(w.find(sel('page-approval')).exists()).toBe(true)
    const v = w.get(sel('approval-version'))
    expect(v.attributes('data-version-id')).toBe(V1)
    expect(v.attributes('data-value')).toBe('3')
    expect(w.get(sel('approval-audience-sms')).attributes('data-value')).toBe('350')
    expect(w.get(sel('approval-audience-email')).attributes('data-value')).toBe('368')
    expect(a.count('GET', `${C}/preview`)).toBeGreaterThanOrEqual(1)
    expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('ready')
  })

  it('without a version the approve button is disabled with reason no_content', async () => {
    const { w } = await mountApproval({ camp: { value: campaign('audience_ready', null) } })
    expect(w.get(sel('approval-approve')).attributes('aria-disabled')).toBe('true')
    expect(w.get(sel('approval-reason')).attributes('data-reason')).toBe('no_content')
  })

  it('approve opens the approve dialog and POSTs the latest version id; success shows the send button', async () => {
    const { w, a } = await mountApproval({
      camp: { value: campaign('content_ready', version(V1, 1)) },
    })
    await w.get(sel('approval-approve')).trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(q('confirm-dialog')?.getAttribute('data-kind')).toBe('approve'))
    expect(a.count('POST', `${C}/approve`)).toBe(0)
    q('confirm-dialog-confirm')?.click()
    await flushPromises()
    expect(a.last('POST', `${C}/approve`)?.opts?.body).toEqual({ content_version_id: V1 })
    await vi.waitFor(() =>
      expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('approved'),
    )
    expect(w.find(sel('approval-send')).exists()).toBe(true)
    await vi.waitFor(() => expect(q('confirm-dialog')).toBeNull())
  })

  it('an approval-time 422 keeps the dialog open with the worst-case field errors (REQ-CNT-05)', async () => {
    const { w } = await mountApproval({
      camp: { value: campaign('content_ready', version()) },
      approve: () => {
        throw apiError(422, 'validation_failed', [
          fieldError('sms.body', 'sms_too_long_worst_case', 160, 163),
        ])
      },
    })
    await openAndConfirm(w, 'approval-approve')
    await vi.waitFor(() => expect(q('confirm-dialog-error')).not.toBeNull())
    const codes = [...(q('confirm-dialog-error')?.querySelectorAll(sel('field-error')) ?? [])].map(
      (e) => e.getAttribute('data-code'),
    )
    expect(codes).toContain('sms_too_long_worst_case')
    expect(q('confirm-dialog')).not.toBeNull()
  })

  it('409 stale_content_version closes the dialog, refetches and shows the stale bar (REQ-APR-03)', async () => {
    const setup: Setup = {
      camp: { value: campaign('content_ready', version()) },
      approve: () => {
        throw apiError(409, 'stale_content_version')
      },
    }
    const { w, a } = await mountApproval(setup)
    const gets = a.count('GET', C)
    await openAndConfirm(w, 'approval-approve')
    await vi.waitFor(() =>
      expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('stale'),
    )
    expect(w.find(sel('approval-stale-note')).exists()).toBe(true)
    expect(a.count('GET', C)).toBeGreaterThan(gets)
    await vi.waitFor(() => expect(q('confirm-dialog')).toBeNull())
  })

  it('send opens the send dialog with the audience counts and POSTs the approved version; success goes to tracking', async () => {
    const { w, a, router } = await mountApproval({
      camp: { value: campaign('approved', version(V1, 1)) },
    })
    await w.get(sel('approval-send')).trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(q('confirm-dialog')?.getAttribute('data-kind')).toBe('send'))
    expect(q('confirm-dialog-count-sms')?.getAttribute('data-value')).toBe('350')
    expect(q('confirm-dialog-count-email')?.getAttribute('data-value')).toBe('368')
    q('confirm-dialog-confirm')?.click()
    await flushPromises()
    expect(a.last('POST', `${C}/send`)?.opts?.body).toEqual({ content_version_id: V1 })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('tracking'))
  })

  it('a stale send also shows the stale bar', async () => {
    const { w } = await mountApproval({
      camp: { value: campaign('approved', version()) },
      send: () => {
        throw apiError(409, 'stale_content_version')
      },
    })
    await openAndConfirm(w, 'approval-send')
    await vi.waitFor(() =>
      expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('stale'),
    )
  })
})

// ---- REQ-SCH-04 C4, C6: planning from the approval page ----

// Now = 2026-01-15T12:00:00Z (15:00 TSİ), fixed before mounting.
const NOW = '2026-01-15T12:00:00Z'
const PLANNED = '2026-01-15T15:30:00Z'

function scheduledCampaign(at = PLANNED): Campaign {
  return campaign('scheduled', version(V1, 1), {
    approval: { id: V3, content_version_id: V1, approved_at: T0 },
    scheduled_at: at,
  } as Partial<Campaign>)
}

function toastWith(messageId: string): HTMLElement | undefined {
  return [...document.querySelectorAll(sel('toast'))].find(
    (e) => e.getAttribute('data-message-id') === messageId,
  ) as HTMLElement | undefined
}

async function fillAt(value: string) {
  const el = q('confirm-dialog-scheduled-at') as HTMLInputElement | null
  if (!el) throw new Error('confirm-dialog-scheduled-at is not in the DOM')
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  el.dispatchEvent(new Event('change', { bubbles: true }))
  await flushPromises()
}

describe('ApprovalPage planning (REQ-SCH-04 C4, C6)', () => {
  beforeEach(() => {
    vi.useFakeTimers({
      toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'],
    })
    vi.setSystemTime(new Date(NOW))
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  async function openSendLater(w: VueWrapper) {
    await w.get(sel('approval-send')).trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(q('confirm-dialog')?.getAttribute('data-kind')).toBe('send'))
    q('confirm-dialog-send-later')?.click()
    await flushPromises()
    await vi.waitFor(() => expect(q('confirm-dialog')?.getAttribute('data-mode')).toBe('later'))
  }

  it('the send dialog offers planning; schedule POSTs scheduled_at, goes to tracking and toasts toast.scheduled', async () => {
    const { w, a, router } = await mountApproval({
      camp: { value: campaign('approved', version(V1, 1)) },
      send: () => ({
        campaign_id: CAMPAIGN_ID,
        state: 'scheduled',
        content_version_id: V1,
        scheduled_at: '2026-01-15T13:30:00Z',
        deliveries: { total: 0, sms: 0, email: 0 },
      }),
    })
    await openSendLater(w)
    await fillAt('2026-01-15T16:30')
    q('confirm-dialog-confirm')?.click()
    await flushPromises()
    expect(a.count('POST', `${C}/send`)).toBe(1)
    expect(a.last('POST', `${C}/send`)?.opts?.body).toEqual({
      content_version_id: V1,
      scheduled_at: '2026-01-15T13:30:00Z',
    })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('tracking'))
    await vi.waitFor(() => expect(toastWith('toast.scheduled')).toBeDefined())
    expect(toastWith('toast.scheduled')?.getAttribute('data-variant')).toBe('success')
  })

  it('a 422 schedule_too_soon keeps the dialog open with the field error', async () => {
    const { w, router } = await mountApproval({
      camp: { value: campaign('approved', version(V1, 1)) },
      send: () => {
        throw apiError(422, 'validation_failed', [fieldError('scheduled_at', 'schedule_too_soon')])
      },
    })
    await openSendLater(w)
    await fillAt('2026-01-15T16:30')
    q('confirm-dialog-confirm')?.click()
    await flushPromises()
    await vi.waitFor(() => expect(q('confirm-dialog-error')).not.toBeNull())
    const codes = [...(q('confirm-dialog-error')?.querySelectorAll(sel('field-error')) ?? [])].map(
      (e) => e.getAttribute('data-code'),
    )
    expect(codes).toContain('schedule_too_soon')
    expect(q('confirm-dialog')).not.toBeNull()
    expect(router.currentRoute.value.name).toBe('approval')
  })

  it('a scheduled campaign shows the scheduled bar and note, without approve, send or edit', async () => {
    const { w } = await mountApproval({ camp: { value: scheduledCampaign() } })
    expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('scheduled')
    for (const id of ['approval-approve', 'approval-send', 'approval-edit']) {
      expect(w.find(sel(id)).exists(), id).toBe(false)
    }
    const note = w.get(sel('approval-scheduled-note'))
    expect(note.attributes('data-value')).toBe(PLANNED)
    expect(note.text().trim()).toBe(t('approval.scheduledNote', { time: formatTsi(PLANNED) }))
  })

  it('approval-schedule-cancel DELETEs the plan without a dialog, reloads, toasts and shows approved', async () => {
    const setup: Setup = { camp: { value: scheduledCampaign() } }
    setup.cancelSchedule = () => {
      setup.camp.value = campaign('approved', version(V1, 1))
      return { campaign_id: CAMPAIGN_ID, state: 'approved', scheduled_at: null }
    }
    const { w, a } = await mountApproval(setup)
    const gets = a.count('GET', C)
    await w.get(sel('approval-schedule-cancel')).trigger('click')
    await flushPromises()
    expect(q('confirm-dialog')).toBeNull()
    expect(a.count('DELETE', `${C}/schedule`)).toBe(1)
    await vi.waitFor(() =>
      expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('approved'),
    )
    expect(a.count('GET', C)).toBeGreaterThan(gets)
    await vi.waitFor(() => expect(toastWith('toast.scheduleCanceled')).toBeDefined())
    expect(toastWith('toast.scheduleCanceled')?.getAttribute('data-variant')).toBe('info')
  })

  it('approval-to-tracking goes to the tracking route', async () => {
    const { w, router } = await mountApproval({ camp: { value: scheduledCampaign() } })
    await w.get(sel('approval-to-tracking')).trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('tracking'))
  })
})
