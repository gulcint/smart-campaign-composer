// Incremental text/event-stream parser (docs/contracts/ui.md §5.1, sse-events.md §0).
// Used by the fetch-based content stream (P4); kept transport-agnostic.

export interface SSEMessage {
  event: string
  data: string
  id: string | null
}

export function createSSEParser(onMessage: (m: SSEMessage) => void): {
  push(chunk: string): void
  end(): void
} {
  let buffer = ''
  let event = ''
  let data: string[] = []
  let id: string | null = null
  let hasData = false
  // A chunk may end right after '\r'; the next chunk's leading '\n' belongs to the same break.
  let skipLeadingLF = false

  const reset = () => {
    event = ''
    data = []
    id = null
    hasData = false
  }

  const dispatch = () => {
    if (hasData) onMessage({ event: event || 'message', data: data.join('\n'), id })
    reset()
  }

  const processLine = (line: string) => {
    if (line === '') {
      dispatch()
      return
    }
    if (line.startsWith(':')) return
    const colon = line.indexOf(':')
    const field = colon === -1 ? line : line.slice(0, colon)
    let value = colon === -1 ? '' : line.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    switch (field) {
      case 'event':
        event = value
        break
      case 'data':
        data.push(value)
        hasData = true
        break
      case 'id':
        id = value
        break
      default:
        // `retry:` and unknown fields are ignored.
        break
    }
  }

  return {
    push(chunk: string) {
      if (skipLeadingLF && chunk.startsWith('\n')) chunk = chunk.slice(1)
      skipLeadingLF = false
      buffer += chunk
      let start = 0
      for (let i = 0; i < buffer.length; i++) {
        const ch = buffer[i]
        if (ch !== '\n' && ch !== '\r') continue
        processLine(buffer.slice(start, i))
        if (ch === '\r') {
          if (i + 1 < buffer.length) {
            if (buffer[i + 1] === '\n') i++
          } else {
            skipLeadingLF = true
          }
        }
        start = i + 1
      }
      buffer = buffer.slice(start)
    },
    end() {
      // An unterminated message is discarded.
      buffer = ''
      skipLeadingLF = false
      reset()
    },
  }
}
