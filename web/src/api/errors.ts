// Error model and error-code -> presentation mapping (docs/contracts/ui.md §4).
import type { MessageId } from '@/i18n'

import type { ErrorCode, ErrorResponse, FieldError } from './types'

export interface ApiError {
  status: number
  code: ErrorCode | 'network_error'
  message: string
  fields: FieldError[]
  requestId: string | null
}

export type Display = 'page' | 'toast' | 'inline' | 'card' | 'banner'
export type ErrorContext = 'brief' | 'audience' | 'content' | 'approval' | 'send' | 'tracking'

interface Rule {
  display: Display
  variant: 'warning' | 'error'
}

const RULES: Record<ApiError['code'], Rule> = {
  campaign_not_found: { display: 'page', variant: 'error' },
  not_found: { display: 'page', variant: 'error' },
  method_not_allowed: { display: 'toast', variant: 'error' },
  content_version_not_found: { display: 'toast', variant: 'error' },
  customer_not_found: { display: 'toast', variant: 'warning' },
  invalid_state: { display: 'toast', variant: 'warning' },
  content_not_validated: { display: 'toast', variant: 'warning' },
  stale_content_version: { display: 'toast', variant: 'warning' },
  content_edit_conflict: { display: 'inline', variant: 'warning' },
  generation_in_progress: { display: 'toast', variant: 'warning' },
  payload_too_large: { display: 'inline', variant: 'error' },
  validation_failed: { display: 'inline', variant: 'error' },
  llm_invalid_output: { display: 'card', variant: 'error' },
  llm_unavailable: { display: 'card', variant: 'error' },
  malformed_request: { display: 'toast', variant: 'error' },
  internal_error: { display: 'toast', variant: 'error' },
  network_error: { display: 'banner', variant: 'error' },
}

/**
 * Maps an API error to how the UI presents it. `context` only matters for 413/422
 * (which field the inline error belongs to — decided by the caller) and for
 * stale_content_version in 'approval' (ApprovalBar stale); the returned shape is the same.
 */
export function errorPresentation(
  e: ApiError,
  context?: ErrorContext,
): { display: Display; messageId: MessageId; variant: 'warning' | 'error' } {
  void context
  const rule = RULES[e.code] ?? { display: 'toast', variant: 'error' }
  const messageId = (e.code === 'network_error' ? 'error.network' : `error.${e.code}`) as MessageId
  return { display: rule.display, messageId, variant: rule.variant }
}

export function isApiError(v: unknown): v is ApiError {
  return (
    typeof v === 'object' &&
    v !== null &&
    typeof (v as ApiError).status === 'number' &&
    typeof (v as ApiError).code === 'string' &&
    Array.isArray((v as ApiError).fields)
  )
}

export function networkError(cause: unknown): ApiError {
  return {
    status: 0,
    code: 'network_error',
    message: cause instanceof Error ? cause.message : 'network error',
    fields: [],
    requestId: null,
  }
}

/** Builds an ApiError from a non-2xx response body (ErrorResponse) or falls back to internal_error. */
export function apiErrorFromBody(
  status: number,
  body: unknown,
  requestId: string | null,
): ApiError {
  const err = (body as Partial<ErrorResponse> | null)?.error
  if (err && typeof err.code === 'string') {
    return {
      status,
      code: err.code,
      message: typeof err.message === 'string' ? err.message : '',
      fields: Array.isArray(err.fields) ? err.fields : [],
      requestId: typeof err.request_id === 'string' ? err.request_id : requestId,
    }
  }
  return {
    status,
    code: status === 404 ? 'not_found' : 'internal_error',
    message: `HTTP ${status}`,
    fields: [],
    requestId,
  }
}

/** Normalises anything thrown by the API layer into an ApiError. */
export function toApiError(e: unknown): ApiError {
  return isApiError(e) ? e : networkError(e)
}
