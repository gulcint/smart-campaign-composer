// REQ-AUD-03, REQ-AUD-04 (UI): the audience card shows how many people the campaign reaches,
// per channel after consent (docs/contracts/ui.md §3.7).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AudienceSummaryCard from '@/components/AudienceSummaryCard.vue'
import type { ApiError } from '@/api/errors'
import { t } from '@/i18n/index'

const sel = (id: string) => `[data-testid="${id}"]`
const fmt = (n: number) => new Intl.NumberFormat('tr-TR').format(n)

const audience = { matched: 1482, sms: 350, email: 368 }

function mountCard(props: Record<string, unknown>) {
  return mount(AudienceSummaryCard, {
    props: { audience: null, loading: false, error: null, ...props },
  })
}

describe('AudienceSummaryCard (REQ-AUD-03, REQ-AUD-04)', () => {
  it('loading state', () => {
    const w = mountCard({ loading: true })
    expect(w.get(sel('audience-card')).attributes('data-state')).toBe('loading')
    expect(w.find(sel('audience-count-matched')).exists()).toBe(false)
  })

  it('ready state shows matched, SMS and email counts with raw data-value and tr-TR text', () => {
    const w = mountCard({ audience })
    expect(w.get(sel('audience-card')).attributes('data-state')).toBe('ready')
    for (const [key, n] of [
      ['matched', audience.matched],
      ['sms', audience.sms],
      ['email', audience.email],
    ] as const) {
      const el = w.get(sel(`audience-count-${key}`))
      expect(el.attributes('data-value')).toBe(String(n))
      expect(el.text()).toContain(fmt(n))
    }
  })

  it('per-channel bars are progressbars scaled to the matched count', () => {
    const w = mountCard({ audience })
    for (const [ch, n] of [
      ['sms', audience.sms],
      ['email', audience.email],
    ] as const) {
      const bar = w.get(sel(`audience-bar-${ch}`))
      expect(bar.attributes('role')).toBe('progressbar')
      expect(bar.attributes('aria-valuenow')).toBe(String(n))
      expect(bar.attributes('aria-valuemax')).toBe(String(audience.matched))
    }
  })

  it('channel counts are independent: SMS-only consent does not raise the email count', () => {
    const w = mountCard({ audience: { matched: 10, sms: 8, email: 0 } })
    expect(w.get(sel('audience-count-sms')).attributes('data-value')).toBe('8')
    expect(w.get(sel('audience-count-email')).attributes('data-value')).toBe('0')
    expect(w.get(sel('audience-bar-email')).attributes('aria-valuenow')).toBe('0')
  })

  it.each([
    { name: 'ready', audience },
    { name: 'empty', audience: { matched: 0, sms: 0, email: 0 } },
  ])('the consent note is shown when $name', ({ audience: a }) => {
    const w = mountCard({ audience: a })
    expect(w.get(sel('audience-consent-note')).text()).toBe(t('audience.consentNote'))
  })

  it('matched = 0 is the empty state and still shows the zero counts', () => {
    const w = mountCard({ audience: { matched: 0, sms: 0, email: 0 } })
    expect(w.get(sel('audience-card')).attributes('data-state')).toBe('empty')
    expect(w.find(sel('audience-empty')).exists()).toBe(true)
    expect(w.get(sel('audience-count-matched')).attributes('data-value')).toBe('0')
  })

  it('error state shows an ErrorCard with the error code', () => {
    const error: ApiError = {
      status: 502,
      code: 'llm_invalid_output',
      message: 'llm output invalid',
      fields: [],
      requestId: 'r-1',
      currentVersion: null,
    }
    const w = mountCard({ error })
    expect(w.get(sel('audience-card')).attributes('data-state')).toBe('error')
    expect(w.get(sel('error-card')).attributes('data-code')).toBe('llm_invalid_output')
    expect(w.find(sel('audience-count-matched')).exists()).toBe(false)
  })
})
