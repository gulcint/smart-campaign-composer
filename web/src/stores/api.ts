// Shared plumbing for stores: the injected API client and the generic error reporter
// (ui.md §4 display rules for toast/banner errors).
import { inject } from 'vue'

import type { ApiClient } from '@/api/client'
import { ApiClientKey, createApiClient } from '@/api/client'
import type { ApiError, ErrorContext } from '@/api/errors'
import { errorPresentation } from '@/api/errors'
import { useNotify } from '@/composables/useNotify'

import { useUiStore } from './ui'

let fallback: ApiClient | null = null

/** Must be called during store setup (Pinia runs it inside the app's injection context). */
export function injectApiClient(): ApiClient {
  const injected = inject(ApiClientKey, null)
  if (injected) return injected
  fallback ??= createApiClient()
  return fallback
}

/**
 * Handles the errors whose presentation does not depend on the page: toasts and the
 * connection banner. Returns true when the error was fully handled here.
 */
export function reportError(e: ApiError, context?: ErrorContext): boolean {
  const p = errorPresentation(e, context)
  if (p.display === 'banner') {
    useUiStore().connectionLost = true
    return true
  }
  if (p.display === 'toast') {
    useNotify().notify({ variant: p.variant, messageId: p.messageId })
    return true
  }
  return false
}
