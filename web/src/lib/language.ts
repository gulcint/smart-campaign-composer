// P8 (REQ-LNG-04): the content language shared between the content and approval steps via the
// `lang` route query. Anything but `en` (missing, invalid, repeated) means Turkish.
import type { LocationQueryValue } from 'vue-router'

export type ContentLanguage = 'tr' | 'en'

export function languageFromQuery(v: LocationQueryValue | LocationQueryValue[] | undefined) {
  return v === 'en' ? 'en' : 'tr'
}
