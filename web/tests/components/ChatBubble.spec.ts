// REQ-AUD-00 (UI brief/assistant exchange), REQ-UI-01: ChatBubble (docs/contracts/ui.md §3.6, §6.1).
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ChatBubble from '@/components/ChatBubble.vue'

const sel = (id: string) => `[data-testid="${id}"]`

describe('ChatBubble', () => {
  it.each(['user', 'assistant'] as const)('carries data-role=%s and the text', (role) => {
    const w = mount(ChatBubble, { props: { role, text: 'Merhaba İstanbul' } })
    expect(w.get(sel('chat-bubble')).attributes('data-role')).toBe(role)
    expect(w.get(sel('chat-bubble-text')).element.textContent).toBe('Merhaba İstanbul')
    expect(w.find(sel('chat-bubble-pending')).exists()).toBe(false)
  })

  it('shows the pending marker only while pending', async () => {
    const w = mount(ChatBubble, { props: { role: 'assistant', text: '', pending: true } })
    expect(w.find(sel('chat-bubble-pending')).exists()).toBe(true)
    await w.setProps({ pending: false, text: 'Kitle hazır' })
    expect(w.find(sel('chat-bubble-pending')).exists()).toBe(false)
  })

  it('renders text as text, never as HTML (REQ-UI-01)', () => {
    const P = '<script>alert(1)</script><b>x</b>'
    const w = mount(ChatBubble, { props: { role: 'assistant', text: P } })
    const el = w.get(sel('chat-bubble-text')).element
    expect(el.textContent).toBe(P)
    expect(el.querySelector('script')).toBeNull()
    expect(el.querySelector('b')).toBeNull()
    expect(el.innerHTML).toContain('&lt;script&gt;')
  })
})
