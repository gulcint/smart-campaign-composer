// REQ-AUD-03, REQ-AUD-04 (UI): the audience card shows how many people the campaign reaches,
// per channel after consent.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import AudienceSummaryCard from '@/components/AudienceSummaryCard.vue'

const sel = (id: string) => `[data-testid="${id}"]`
const fmt = (n: number) => new Intl.NumberFormat('tr-TR').format(n)

const audience = { matched: 1482, sms: 350, email: 368 }

function mountCard(props: Record<string, unknown>) {
  return mount(AudienceSummaryCard, {
    props: { audience: null, loading: false, error: null, ...props },
  })
}

describe('AudienceSummaryCard (REQ-AUD-03, REQ-AUD-04)', () => {
  it('shows matched, SMS and email counts', () => {
    const w = mountCard({ audience })
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

  it('channel counts are independent: SMS-only consent does not raise the email count', () => {
    const w = mountCard({ audience: { matched: 10, sms: 8, email: 0 } })
    expect(w.get(sel('audience-count-sms')).attributes('data-value')).toBe('8')
    expect(w.get(sel('audience-count-email')).attributes('data-value')).toBe('0')
  })
})
