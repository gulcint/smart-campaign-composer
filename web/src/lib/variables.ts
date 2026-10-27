// Personalisation variables on the client (ui.md §3.12). Presentation only: the backend
// validator (`content.Validate`) is the authority and reports the real errors.

/** Variables the editor offers as insert chips (and the only ones the backend accepts). */
export const ALLOWED_VARIABLES = ['first_name', 'last_name', 'city'] as const

const VARIABLE_RE = /\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g

/** Unique names of well-formed `{{name}}` variables that are not allowed, in order of appearance. */
export function invalidVariables(...texts: string[]): string[] {
  const allowed = new Set<string>(ALLOWED_VARIABLES)
  const seen = new Set<string>()
  for (const text of texts) {
    for (const m of text.matchAll(VARIABLE_RE)) {
      const name = m[1] as string
      if (!allowed.has(name)) seen.add(name)
    }
  }
  return [...seen]
}
