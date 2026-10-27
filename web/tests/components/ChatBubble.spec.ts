// REQ-UI-01: ChatBubble renders LLM/edited text as text, never as HTML.
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ChatBubble from '@/components/ChatBubble.vue'

const sel = (id: string) => `[data-testid="${id}"]`

describe('ChatBubble', () => {
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
