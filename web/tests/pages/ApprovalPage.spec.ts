// REQ-APR-01, REQ-APR-03, REQ-CNT-05 (UI): ApprovalPage composition (docs/contracts/ui.md §3.24,
// §3.17, §3.18, §4).
import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import ApprovalPage from '@/pages/ApprovalPage.vue'
import type { Campaign } from '@/api/types'
import { mountPage } from '../helpers/pages'
import {
  CAMPAIGN_ID,
  V1,
  apiError,
  campaign,
  fakeApi,
  fieldError,
  preview,
  sel,
  version,
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
}

async function mountApproval(s: Setup) {
  const a = fakeApi({
    [`GET ${C}`]: () => s.camp.value,
    [`GET ${C}/preview`]: () => preview(),
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
  const r = await mountPage(ApprovalPage, 'approval', { api: a.client })
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

// D49 (ui.md §1.1 preview triggers, §3.15; openapi previewContent): sms-preview-next requests
// sample_index = (i + 1) mod min(sample_count, 5) — the UI walks only the first 5 samples.
describe('ApprovalPage next sample (REQ-APR-01, D49)', () => {
  function requestedIndex(path: string, opts?: { query?: Record<string, string | number> }) {
    const fromQuery = opts?.query?.sample_index
    const fromPath = new URLSearchParams(path.split('?')[1] ?? '').get('sample_index')
    return Number(fromQuery ?? fromPath ?? 0)
  }

  async function mountWithSample(index: number, count: number) {
    const requested: number[] = []
    const a = fakeApi({
      [`GET ${C}`]: () => campaign('content_ready', version(V1, 3)),
      [`GET ${C}/preview`]: () => preview({ sample_index: index, sample_count: count }),
    })
    const client = {
      request(method: string, path: string, opts?: { query?: Record<string, string | number> }) {
        if (method.toUpperCase() === 'GET' && path.split('?')[0]?.endsWith('/preview')) {
          requested.push(requestedIndex(path, opts))
        }
        return a.client.request(method, path, opts as never)
      },
    }
    const r = await mountPage(ApprovalPage, 'approval', { api: client })
    wrapper = r.wrapper
    return { w: r.wrapper, requested }
  }

  it.each([
    { index: 0, count: 584, want: 1 },
    { index: 3, count: 584, want: 4 },
    { index: 4, count: 584, want: 0 },
    { index: 2, count: 3, want: 0 },
    { index: 1, count: 3, want: 2 },
  ])(
    'from sample_index $index of $count, next requests sample_index=$want',
    async ({ index, count, want }) => {
      const { w, requested } = await mountWithSample(index, count)
      expect(requested.length).toBeGreaterThanOrEqual(1)
      const before = requested.length
      await w.get(sel('sms-preview-next')).trigger('click')
      await flushPromises()
      await vi.waitFor(() => expect(requested.length).toBeGreaterThan(before))
      expect(requested[requested.length - 1]).toBe(want)
    },
  )
})
