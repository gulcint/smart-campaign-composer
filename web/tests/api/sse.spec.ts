// REQ-CNT-04 (UI): the SSE parser rules of docs/contracts/ui.md §5.1.
import { describe, expect, it } from 'vitest'
import { createSSEParser, type SSEMessage } from '@/api/sse'

function parse(chunks: string[], end = false): SSEMessage[] {
  const out: SSEMessage[] = []
  const p = createSSEParser((m) => out.push(m))
  for (const c of chunks) p.push(c)
  if (end) p.end()
  return out
}

describe('createSSEParser (ui.md §5.1)', () => {
  it('parses event and data; a blank line completes the message', () => {
    expect(parse(['event: delta\ndata: {"a":1}\n\n'])).toEqual([
      { event: 'delta', data: '{"a":1}', id: null },
    ])
  })

  it.each([
    { name: 'CRLF', s: 'event: delta\r\ndata: x\r\n\r\n' },
    { name: 'CR', s: 'event: delta\rdata: x\r\r' },
  ])('accepts $name line endings', ({ s }) => {
    expect(parse([s])).toEqual([{ event: 'delta', data: 'x', id: null }])
  })

  it('handles chunks split anywhere, including inside a CRLF', () => {
    const s = 'event: validated\r\ndata: {"b":2}\r\n\r\n'
    const chunks = [...s].map((ch) => ch)
    expect(parse(chunks)).toEqual([{ event: 'validated', data: '{"b":2}', id: null }])
  })

  it('ignores comment lines and retry', () => {
    expect(parse([': keepalive\n\nretry: 3000\n\nevent: delta\ndata: x\n\n'])).toEqual([
      { event: 'delta', data: 'x', id: null },
    ])
  })

  it('defaults the event name to message and keeps the id', () => {
    expect(parse(['id: 7\ndata: x\n\n'])).toEqual([{ event: 'message', data: 'x', id: '7' }])
  })

  it('joins several data lines with \\n', () => {
    expect(parse(['data: a\ndata: b\n\n'])).toEqual([{ event: 'message', data: 'a\nb', id: null }])
  })

  it('end() drops an unterminated message', () => {
    expect(parse(['event: delta\ndata: x\n\nevent: validated\ndata: y\n'], true)).toEqual([
      { event: 'delta', data: 'x', id: null },
    ])
  })
})
