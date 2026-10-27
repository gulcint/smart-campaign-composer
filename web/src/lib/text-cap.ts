// Input cap for the SMS body and the email subject (docs/contracts/ui.md §3.14 d2, D48).
// Counting is the CharCounter's (§3.13): NFC runes of the template text, never UTF-16 units.

/** NFC rune count, same as CharCounter. */
export function runeCount(text: string): number {
  return [...text.normalize('NFC')].length
}

/**
 * The longest rune prefix of `inserted` such that `before + prefix + after` stays within the cap.
 * The cap is `limit`, or the current count when the text is already over it (an invalid draft):
 * then only inputs that do not increase the count are applied.
 */
export function fitInsertion(
  before: string,
  inserted: string,
  after: string,
  limit: number,
  current: number,
): string {
  const cap = Math.max(limit, current)
  if (runeCount(before + inserted + after) <= cap) return inserted
  const chars = [...inserted]
  // Adding code points never lowers the NFC count, so the fitting length is found by bisection.
  let lo = 0
  let hi = chars.length - 1
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2)
    if (runeCount(before + chars.slice(0, mid).join('') + after) <= cap) lo = mid
    else hi = mid - 1
  }
  return chars.slice(0, lo).join('')
}

const isHigh = (c: number) => c >= 0xd800 && c <= 0xdbff
const isLow = (c: number) => c >= 0xdc00 && c <= 0xdfff

/**
 * The edit that turned `prev` into `next`, as common prefix / suffix lengths (UTF-16 units, never
 * splitting a surrogate pair). Used when only the resulting value is known (IME, drop, autocorrect).
 */
export function diffEdit(
  prev: string,
  next: string,
): { start: number; prevEnd: number; nextEnd: number } {
  const max = Math.min(prev.length, next.length)
  let p = 0
  while (p < max && prev.charCodeAt(p) === next.charCodeAt(p)) p++
  if (p > 0 && isHigh(prev.charCodeAt(p - 1))) p--
  let s = 0
  while (
    s < max - p &&
    prev.charCodeAt(prev.length - 1 - s) === next.charCodeAt(next.length - 1 - s)
  )
    s++
  if (s > 0 && isLow(prev.charCodeAt(prev.length - s))) s--
  return { start: p, prevEnd: prev.length - s, nextEnd: next.length - s }
}
