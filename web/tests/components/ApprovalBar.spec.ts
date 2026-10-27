// REQ-APR-01, REQ-CNT-04 (UI): approval only for validated content; send only after approval.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ApprovalBar from '@/components/ApprovalBar.vue'
import { sel } from '../helpers/p4'

function mountBar(props: Record<string, unknown>) {
  return mount(ApprovalBar, {
    props: {
      campaignState: 'content_ready',
      versionStatus: 'validated',
      dirty: false,
      submitting: false,
      stale: false,
      ...props,
    },
    attachTo: document.body,
  })
}

describe('ApprovalBar (REQ-APR-01, REQ-CNT-04)', () => {
  it('validated content_ready => approve enabled and emits approve; no send yet', async () => {
    const w = mountBar({})
    const btn = w.get(sel('approval-approve'))
    expect(btn.attributes('aria-disabled')).not.toBe('true')
    await btn.trigger('click')
    expect(w.emitted('approve')).toHaveLength(1)
    expect(w.emitted('send')).toBeUndefined()
    w.unmount()
  })

  it('approved: send emits send', async () => {
    const w = mountBar({ campaignState: 'approved' })
    await w.get(sel('approval-send')).trigger('click')
    expect(w.emitted('send')).toHaveLength(1)
    w.unmount()
  })

  it.each([
    { name: 'no content', props: { versionStatus: 'none' } },
    { name: 'streaming', props: { versionStatus: 'streaming' } },
    { name: 'invalid', props: { versionStatus: 'invalid' } },
    { name: 'interrupted', props: { versionStatus: 'interrupted' } },
  ])('approve is disabled and does not emit when $name (no validated event)', async ({ props }) => {
    const w = mountBar(props)
    const btn = w.get(sel('approval-approve'))
    expect(btn.attributes('aria-disabled')).toBe('true')
    await btn.trigger('click')
    expect(w.emitted('approve')).toBeUndefined()
    w.unmount()
  })
})
