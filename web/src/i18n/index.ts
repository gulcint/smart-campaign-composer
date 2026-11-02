// Minimal typed i18n (DESIGN.md §10): one Turkish message table, `t(id, params)`.
import { tr } from './tr'

export type MessageId = keyof typeof tr

export const messages: Record<MessageId, string> = tr

export function hasMessage(id: string): id is MessageId {
  return Object.prototype.hasOwnProperty.call(messages, id)
}

/** Returns the message for `id` with `{name}` placeholders replaced; unknown params stay as-is. */
export function t(id: MessageId, params?: Record<string, string | number>): string {
  const template = messages[id] ?? id
  if (!params) return template
  return template.replace(/\{(\w+)\}/g, (match, key: string) =>
    Object.prototype.hasOwnProperty.call(params, key) ? String(params[key]) : match,
  )
}

const numberFormat = new Intl.NumberFormat('tr-TR')

/** Numbers on screen use tr-TR grouping (ui.md §0): 1482 -> "1.482". */
export function formatNumber(n: number): string {
  return numberFormat.format(n)
}

/**
 * The message for `id` split at its `{name}` placeholders, so a template can wrap each value in its
 * own element (a testid, a numeral style) while the joined text still equals `t(id, params)`.
 */
export function messageParts(id: MessageId): { text: string; key: string | null }[] {
  const out: { text: string; key: string | null }[] = []
  const template = messages[id] ?? id
  let last = 0
  for (const m of template.matchAll(/\{(\w+)\}/g)) {
    if (m.index > last) out.push({ text: template.slice(last, m.index), key: null })
    out.push({ text: '', key: m[1] ?? '' })
    last = m.index + m[0].length
  }
  if (last < template.length) out.push({ text: template.slice(last), key: null })
  return out
}
