// REQ-DSH-02 C2 (UI): `formatTsiPlain(iso)` of `@/lib/schedule.ts` is the TSİ (Europe/Istanbul) date
// '<gün> <ay adı> <yıl> <SS>:<DD>' without a zone suffix, independent of the process TZ; the
// existing `formatTsi(iso)` stays `formatTsiPlain(iso) + ' ' + t('schedule.tz')`.
import { afterAll, beforeEach, describe, expect, it } from 'vitest'
import { formatTsi, formatTsiPlain } from '@/lib/schedule'
import { t } from '@/i18n/index'

const originalTZ = process.env.TZ

afterAll(() => {
  if (originalTZ === undefined) delete process.env.TZ
  else process.env.TZ = originalTZ
})

describe.each(['UTC', 'America/New_York', 'Asia/Tokyo'])(
  'formatTsiPlain with TZ=%s (REQ-DSH-02 C2)',
  (tz) => {
    beforeEach(() => {
      process.env.TZ = tz
    })

    // REQ-DSH-02 C2
    it.each([
      { iso: '2026-01-15T12:30:00Z', want: '15 Ocak 2026 15:30' },
      { iso: '2026-01-15T21:05:00Z', want: '16 Ocak 2026 00:05' },
    ])('formatTsiPlain($iso) = $want', ({ iso, want }) => {
      expect(formatTsiPlain(iso)).toBe(want)
    })

    // REQ-DSH-02 C2
    it.each(['2026-01-15T12:30:00Z', '2026-01-15T21:05:00Z', '2026-11-02T09:15:04.120511Z'])(
      'formatTsi(%s) = formatTsiPlain + space + the zone label',
      (iso) => {
        expect(formatTsi(iso)).toBe(formatTsiPlain(iso) + ' ' + t('schedule.tz'))
      },
    )
  },
)
