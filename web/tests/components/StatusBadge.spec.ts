// REQ-SCH-04 C6 (UI): the campaign status badge of a scheduled campaign (StatusBadge kind="campaign",
// status="scheduled") carries data-status="scheduled" and the text t('status.campaign.scheduled').
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import StatusBadge from '@/components/StatusBadge.vue'
import { t } from '@/i18n/index'

describe('StatusBadge (REQ-SCH-04 C6)', () => {
  it('campaign status scheduled', () => {
    const w = mount(StatusBadge, { props: { kind: 'campaign', status: 'scheduled' } })
    const badge = w.element.hasAttribute('data-status')
      ? w.element
      : w.element.querySelector('[data-status]')
    expect(badge?.getAttribute('data-status')).toBe('scheduled')
    expect(w.text()).toContain(t('status.campaign.scheduled'))
    w.unmount()
  })
})
