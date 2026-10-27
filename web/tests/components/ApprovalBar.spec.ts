// REQ-APR-01, REQ-APR-03, REQ-CNT-04 (UI): ApprovalBar states (docs/contracts/ui.md §3.17).
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
  it('ready: validated, clean, content_ready => approve enabled and emits approve', async () => {
    const w = mountBar({})
    expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('ready')
    const btn = w.get(sel('approval-approve'))
    expect(btn.attributes('aria-disabled')).not.toBe('true')
    await btn.trigger('click')
    expect(w.emitted('approve')).toHaveLength(1)
    expect(w.find(sel('approval-reason')).exists()).toBe(false)
    w.unmount()
  })

  it('approved: send button, no approve button; send emits send', async () => {
    const w = mountBar({ campaignState: 'approved' })
    expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('approved')
    expect(w.find(sel('approval-approve')).exists()).toBe(false)
    await w.get(sel('approval-send')).trigger('click')
    expect(w.emitted('send')).toHaveLength(1)
    w.unmount()
  })

  it('stale wins over approved: stale note and re-approval allowed', async () => {
    const w = mountBar({ campaignState: 'approved', stale: true })
    expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('stale')
    expect(w.find(sel('approval-stale-note')).exists()).toBe(true)
    const btn = w.get(sel('approval-approve'))
    expect(btn.attributes('aria-disabled')).not.toBe('true')
    await btn.trigger('click')
    expect(w.emitted('approve')).toHaveLength(1)
    w.unmount()
  })

  it('submitting state', () => {
    const w = mountBar({ submitting: true })
    expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('submitting')
    w.unmount()
  })

  it.each([
    { name: 'no content', props: { versionStatus: 'none' }, reason: 'no_content' },
    { name: 'streaming', props: { versionStatus: 'streaming' }, reason: 'streaming' },
    { name: 'invalid', props: { versionStatus: 'invalid' }, reason: 'invalid' },
    { name: 'interrupted', props: { versionStatus: 'interrupted' }, reason: 'interrupted' },
    { name: 'dirty editor', props: { dirty: true }, reason: 'dirty' },
    {
      name: 'audience_ready campaign',
      props: { campaignState: 'audience_ready' },
      reason: 'state',
    },
  ])(
    'disabled with reason $reason when $name; approve does not emit',
    async ({ props, reason }) => {
      const w = mountBar(props)
      expect(w.get(sel('approval-bar')).attributes('data-state')).toBe('disabled')
      const btn = w.get(sel('approval-approve'))
      expect(btn.attributes('aria-disabled')).toBe('true')
      await btn.trigger('click')
      expect(w.emitted('approve')).toBeUndefined()
      const why = w.get(sel('approval-reason'))
      expect(why.attributes('data-reason')).toBe(reason)
      const ids = (btn.attributes('aria-describedby') ?? '').split(/\s+/)
      expect(ids).toContain(why.attributes('id'))
      w.unmount()
    },
  )
})
