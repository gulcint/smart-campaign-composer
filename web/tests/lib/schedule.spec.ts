// REQ-SCH-04 C2 (UI): the pure time helpers of `@/lib/schedule.ts`. The interface enters and shows
// plan times only in TSİ (Europe/Istanbul); the results do not depend on the process time zone
// (REQ-SCH-04 C1: the same output with TZ = UTC and TZ = America/New_York).
import { afterAll, beforeEach, describe, expect, it } from 'vitest'
import { formatCountdown, formatTsi, remainingSeconds, toTsiInput, toUtcIso } from '@/lib/schedule'
import { t } from '@/i18n/index'

const originalTZ = process.env.TZ

afterAll(() => {
  if (originalTZ === undefined) delete process.env.TZ
  else process.env.TZ = originalTZ
})

describe.each(['UTC', 'America/New_York'])(
  'schedule helpers with TZ=%s (REQ-SCH-04 C1, C2)',
  (tz) => {
    beforeEach(() => {
      process.env.TZ = tz
    })

    it.each([
      { input: '2026-01-15T15:30', want: '2026-01-15T12:30:00Z' },
      { input: '2026-01-16T00:05', want: '2026-01-15T21:05:00Z' },
      { input: '2026-02-14T15:00', want: '2026-02-14T12:00:00Z' },
    ])('toUtcIso($input) = $want', ({ input, want }) => {
      expect(toUtcIso(input)).toBe(want)
    })

    it.each(['', '2026-01-15 15:30', '2026-02-30T10:00'])('toUtcIso(%j) = null', (input) => {
      expect(toUtcIso(input)).toBeNull()
    })

    it.each([
      { iso: '2026-01-15T12:30:45Z', want: '2026-01-15T15:30' },
      { iso: '2026-01-15T21:05:00Z', want: '2026-01-16T00:05' },
    ])('toTsiInput($iso) = $want', ({ iso, want }) => {
      expect(toTsiInput(iso)).toBe(want)
    })

    it.each([
      { iso: '2026-01-15T12:30:00Z', want: '15 Ocak 2026 15:30 ' },
      { iso: '2026-01-15T21:05:00Z', want: '16 Ocak 2026 00:05 ' },
    ])('formatTsi($iso) is the TSİ date with the zone label', ({ iso, want }) => {
      expect(formatTsi(iso)).toBe(want + t('schedule.tz'))
    })

    it.each([
      {
        name: '90 s ahead',
        iso: '2026-01-15T12:01:30Z',
        nowMs: Date.parse('2026-01-15T12:00:00Z'),
        want: 90,
      },
      {
        name: 'a partial second rounds up',
        iso: '2026-01-15T12:00:01Z',
        nowMs: Date.parse('2026-01-15T12:00:00.001Z'),
        want: 1,
      },
      {
        name: 'exactly now is 0',
        iso: '2026-01-15T12:00:00Z',
        nowMs: Date.parse('2026-01-15T12:00:00Z'),
        want: 0,
      },
      {
        name: 'the past is 0, never negative',
        iso: '2026-01-15T11:00:00Z',
        nowMs: Date.parse('2026-01-15T12:00:00Z'),
        want: 0,
      },
    ])('remainingSeconds: $name', ({ iso, nowMs, want }) => {
      expect(remainingSeconds(iso, nowMs)).toBe(want)
    })

    it.each([
      { s: 90061, want: { days: 1, hms: '01:01:01' } },
      { s: 59, want: { days: 0, hms: '00:00:59' } },
    ])('formatCountdown($s)', ({ s, want }) => {
      expect(formatCountdown(s)).toEqual(want)
    })
  },
)
