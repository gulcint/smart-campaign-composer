// REQ-ABT-04 C11 (UI): `messages` of `@/i18n/index.ts` holds a non-empty text for every id the A/B
// screens use. An id is looked up as a flat key first and then as a dotted path (same rule as
// schedule-messages.spec.ts).
import { describe, expect, it } from 'vitest'
import { messages } from '@/i18n/index'

const IDS = [
  'tabs.variants',
  'variant.a',
  'variant.b',
  'content.abToggle',
  'content.abToggleHint',
  'editor.angle',
  'approval.abSplitNote',
  'tracking.variantTitle',
  'tracking.variantNote',
  'field_error.variants_identical',
  'field_error.variant_not_available',
]

function lookup(id: string): unknown {
  const root = messages as unknown as Record<string, unknown>
  if (typeof root[id] === 'string') return root[id]
  let cur: unknown = root
  for (const part of id.split('.')) {
    if (cur === null || typeof cur !== 'object') return undefined
    cur = (cur as Record<string, unknown>)[part]
  }
  return cur
}

describe('A/B messages (REQ-ABT-04 C11)', () => {
  it.each(IDS)('%s has a non-empty text', (id) => {
    const v = lookup(id)
    expect(typeof v, id).toBe('string')
    expect((v as string).trim().length, id).toBeGreaterThan(0)
  })
})
