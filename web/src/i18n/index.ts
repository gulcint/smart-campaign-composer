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
