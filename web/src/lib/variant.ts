// P9 (REQ-ABT-04): the A/B variant shared between the content and approval steps via the
// `variant` route query (the P8 `lang` pattern). Anything but `b` (missing, invalid, repeated)
// means variant A; a page also falls back to A when its version has no variant B.
import type { LocationQueryValue } from 'vue-router'

import type { Content, WorstCase } from '@/api/types'

export type ContentVariant = 'a' | 'b'

/** Angle labels are plain text of at most this many runes (content.AngleMaxRunes). */
export const ANGLE_MAX_RUNES = 60

export function variantFromQuery(v: LocationQueryValue | LocationQueryValue[] | undefined) {
  return v === 'b' ? 'b' : 'a'
}

/** One variant × language copy of a Content (the shape of `en`). */
export type ContentCopy = NonNullable<Content['en']>

/** The copy shown for (variant, language); null when the version has no such copy. */
export function copyOf(
  c: Content,
  variant: ContentVariant,
  language: 'tr' | 'en',
): ContentCopy | null {
  const v: Content | NonNullable<Content['b']> | undefined = variant === 'b' ? c.b : c
  if (!v) return null
  if (language === 'en') return v.en ?? null
  return { sms: v.sms, email: v.email }
}

/** Worst-case lengths of one variant × language copy (null when the version lacks it). */
export function worstOf(
  wc: WorstCase | null | undefined,
  variant: ContentVariant,
  language: 'tr' | 'en',
): { sms_runes: number; subject_runes: number } | null {
  if (!wc) return null
  const v = variant === 'b' ? wc.b : wc
  if (!v) return null
  if (language === 'en') return v.en ?? null
  return { sms_runes: v.sms_runes, subject_runes: v.subject_runes }
}

/** Is `path` a variant-level error (`b`: variants_identical, `angles.a|b`) rather than a field's? */
export function isVariantLevelPath(path: string): boolean {
  return path === 'b' || path === 'angles' || path.startsWith('angles.')
}

/** Does an error path belong to variant B (its fields, `b` itself or `angles.b`)? */
export function isVariantBPath(path: string): boolean {
  return path === 'b' || path.startsWith('b.') || path === 'angles.b'
}
