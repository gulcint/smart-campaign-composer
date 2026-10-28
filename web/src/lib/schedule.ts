// ui.md §3.26: TSİ (Europe/Istanbul) clock helpers for scheduled sends. Pure functions; the
// conversion goes through the IANA zone (Intl), never a hard-coded +03:00 and never the
// process/browser time zone, so results are the same under any `TZ`.
import { t } from '@/i18n'

export const SCHEDULE_TIME_ZONE = 'Europe/Istanbul'

const LOCAL_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/

const wallFormat = new Intl.DateTimeFormat('en-US', {
  timeZone: SCHEDULE_TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
})

const displayFormat = new Intl.DateTimeFormat('tr-TR', {
  timeZone: SCHEDULE_TIME_ZONE,
  day: 'numeric',
  month: 'long',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

type Parts = Record<string, string>

function partsOf(fmt: Intl.DateTimeFormat, ms: number): Parts {
  const out: Parts = {}
  for (const p of fmt.formatToParts(new Date(ms))) out[p.type] = p.value
  return out
}

const pad = (n: number, width = 2) => String(n).padStart(width, '0')

/** Milliseconds the zone's wall clock is ahead of UTC at the instant `ms`. */
function zoneOffsetMs(ms: number): number {
  const p = partsOf(wallFormat, ms)
  const wall = Date.UTC(
    Number(p.year),
    Number(p.month) - 1,
    Number(p.day),
    Number(p.hour),
    Number(p.minute),
    Number(p.second),
  )
  return wall - Math.floor(ms / 1000) * 1000
}

/** TSİ wall-clock 'YYYY-MM-DDTHH:MM' (datetime-local value) -> UTC 'YYYY-MM-DDTHH:MM:00Z'; anything else -> null. */
export function toUtcIso(local: string): string | null {
  const m = LOCAL_RE.exec(local)
  if (!m) return null
  const [y, mo, d, h, mi] = m.slice(1).map(Number) as [number, number, number, number, number]
  const asUtc = Date.UTC(y, mo - 1, d, h, mi)
  const check = new Date(asUtc)
  // Date.UTC rolls 02-30 over to 03-02, so a round trip rejects impossible calendar values.
  if (
    check.getUTCFullYear() !== y ||
    check.getUTCMonth() !== mo - 1 ||
    check.getUTCDate() !== d ||
    h > 23 ||
    mi > 59
  ) {
    return null
  }
  // Two passes settle the offset even when it differs between the guess and the answer.
  const first = asUtc - zoneOffsetMs(asUtc)
  const utc = asUtc - zoneOffsetMs(first)
  return `${new Date(utc).toISOString().slice(0, 16)}:00Z`
}

/** UTC ISO (any RFC 3339 instant) -> TSİ 'YYYY-MM-DDTHH:MM' (seconds dropped, not rounded). */
export function toTsiInput(iso: string): string {
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return ''
  const p = partsOf(wallFormat, ms)
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}`
}

/** UTC ISO -> '<day> <month name> <year> <HH>:<MM> TSİ' (separators are ours, not ICU's). */
export function formatTsi(iso: string): string {
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return iso
  const p = partsOf(displayFormat, ms)
  return `${p.day} ${p.month} ${p.year} ${p.hour}:${p.minute} ${t('schedule.tz')}`
}

/** max(0, ceil((Date.parse(iso) - nowMs) / 1000)) */
export function remainingSeconds(iso: string, nowMs: number): number {
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return 0
  return Math.max(0, Math.ceil((ms - nowMs) / 1000))
}

/** days = floor(s / 86400); hms = zero-padded 'HH:MM:SS' of s mod 86400 */
export function formatCountdown(seconds: number): { days: number; hms: string } {
  const s = Math.max(0, Math.floor(seconds))
  const days = Math.floor(s / 86400)
  const rest = s % 86400
  return {
    days,
    hms: `${pad(Math.floor(rest / 3600))}:${pad(Math.floor((rest % 3600) / 60))}:${pad(rest % 60)}`,
  }
}
