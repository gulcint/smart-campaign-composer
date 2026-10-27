// REQ-CNT-04 (UI): content arrives over SSE, piece by piece; the client parses the events.
import { describe, expect, it } from 'vitest'
import { createSSEParser, type SSEMessage } from '@/api/sse'

function parse(chunks: string[], end = false): SSEMessage[] {
  const out: SSEMessage[] = []
  const p = createSSEParser((m) => out.push(m))
  for (const c of chunks) p.push(c)
  if (end) p.end()
  return out
}

describe('createSSEParser (REQ-CNT-04)', () => {
  it('parses event and data; a blank line completes the message', () => {
    expect(parse(['event: delta\ndata: {"a":1}\n\n'])).toEqual([
      { event: 'delta', data: '{"a":1}', id: null },
    ])
  })

  it('handles a stream that arrives in arbitrary pieces', () => {
    const s = 'event: validated\r\ndata: {"b":2}\r\n\r\n'
    const chunks = [...s].map((ch) => ch)
    expect(parse(chunks)).toEqual([{ event: 'validated', data: '{"b":2}', id: null }])
  })

  it('a stream cut mid-way yields no terminal event: end() drops an unterminated message', () => {
    expect(parse(['event: delta\ndata: x\n\nevent: validated\ndata: y\n'], true)).toEqual([
      { event: 'delta', data: 'x', id: null },
    ])
  })
})
