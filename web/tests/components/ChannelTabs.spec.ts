// REQ-CNT-04, REQ-APR-01 (UI): ChannelTabs status, roles and keyboard (docs/contracts/ui.md §3.9).
import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import ChannelTabs from '@/components/ChannelTabs.vue'
import { t } from '@/i18n/index'
import { sel } from '../helpers/p4'

type Phase = 'idle' | 'streaming' | 'retrying' | 'validated' | 'invalid' | 'interrupted'

let wrapper: VueWrapper | undefined
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

function phases(sms: Phase, subject: Phase, body: Phase) {
  return { 'sms-body': sms, 'email-subject': subject, 'email-body': body }
}

function mountTabs(props: Record<string, unknown> = {}) {
  wrapper = mount(ChannelTabs, {
    props: { modelValue: 'sms', phases: phases('validated', 'validated', 'validated'), ...props },
    attachTo: document.body,
  })
  return wrapper
}

function lastChannel(w: VueWrapper): unknown {
  const ev = w.emitted('update:modelValue') as unknown[][] | undefined
  return ev?.[ev.length - 1]?.[0]
}

describe('ChannelTabs selection and status (REQ-CNT-04)', () => {
  it('marks the selected tab with aria-selected and a roving tabindex', () => {
    const w = mountTabs({ modelValue: 'email' })
    expect(w.get(sel('channel-tab-email')).attributes('aria-selected')).toBe('true')
    expect(w.get(sel('channel-tab-sms')).attributes('aria-selected')).toBe('false')
    expect(w.get(sel('channel-tab-email')).attributes('tabindex')).toBe('0')
    expect(w.get(sel('channel-tab-sms')).attributes('tabindex')).toBe('-1')
  })

  it.each([
    {
      name: 'all validated',
      p: phases('validated', 'validated', 'validated'),
      sms: 'validated',
      email: 'validated',
    },
    {
      name: 'streaming',
      p: phases('streaming', 'streaming', 'streaming'),
      sms: 'streaming',
      email: 'streaming',
    },
    {
      name: 'email body invalid makes email invalid',
      p: phases('validated', 'validated', 'invalid'),
      sms: 'validated',
      email: 'invalid',
    },
    {
      name: 'otherwise the first field decides',
      p: phases('retrying', 'interrupted', 'streaming'),
      sms: 'retrying',
      email: 'interrupted',
    },
  ])('data-status: $name', ({ p, sms, email }) => {
    const w = mountTabs({ phases: p })
    expect(w.get(sel('channel-tab-sms')).attributes('data-status')).toBe(sms)
    expect(w.get(sel('channel-tab-email')).attributes('data-status')).toBe(email)
  })

  it('shows channel-tab-error-<c> only for an invalid channel', () => {
    const w = mountTabs({ phases: phases('invalid', 'validated', 'validated') })
    expect(w.find(sel('channel-tab-error-sms')).exists()).toBe(true)
    expect(w.find(sel('channel-tab-error-email')).exists()).toBe(false)
  })

  it('clicking a tab emits update:modelValue', async () => {
    const w = mountTabs()
    await w.get(sel('channel-tab-email')).trigger('mousedown')
    await w.get(sel('channel-tab-email')).trigger('click')
    expect(lastChannel(w)).toBe('email')
  })
})

describe('ChannelTabs accessibility (ui.md §3.9)', () => {
  it('tablist with a label, tabs controlling their panels, hidden unselected panel kept in the DOM', () => {
    const w = mountTabs({ modelValue: 'sms' })
    const list = w.element.querySelector('[role="tablist"]')
    expect(list).not.toBeNull()
    expect(list?.getAttribute('aria-label')).toBe(t('tabs.channels'))
    for (const c of ['sms', 'email']) {
      const tab = w.get(sel(`channel-tab-${c}`))
      expect(tab.attributes('role')).toBe('tab')
      const panel = w.get(sel(`content-panel-${c}`))
      expect(panel.attributes('role')).toBe('tabpanel')
      expect(tab.attributes('aria-controls')).toBe(panel.attributes('id'))
      expect(panel.attributes('aria-labelledby')).toBe(tab.attributes('id'))
    }
    expect(w.get(sel('content-panel-email')).attributes('hidden')).toBeDefined()
    expect(w.get(sel('content-panel-sms')).attributes('hidden')).toBeUndefined()
  })

  it.each([
    { from: 'sms', key: 'ArrowRight', to: 'email' },
    { from: 'email', key: 'ArrowRight', to: 'sms' },
    { from: 'email', key: 'ArrowLeft', to: 'sms' },
    { from: 'sms', key: 'ArrowLeft', to: 'email' },
    { from: 'email', key: 'Home', to: 'sms' },
    { from: 'sms', key: 'End', to: 'email' },
  ])('$key on $from selects $to (automatic activation, wraps)', async ({ from, key, to }) => {
    const w = mountTabs({ modelValue: from })
    const tab = w.get(sel(`channel-tab-${from}`))
    ;(tab.element as HTMLElement).focus()
    await tab.trigger('keydown', { key })
    expect(lastChannel(w)).toBe(to)
  })
})
