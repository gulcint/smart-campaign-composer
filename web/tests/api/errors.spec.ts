// REQ-AUD-05, REQ-AUD-01, REQ-AI-01, REQ-APR-03 (UI): error code -> presentation -> i18n id
// (docs/contracts/ui.md §4) and the presence of every mandatory message id.
import { describe, expect, it } from 'vitest'
import { errorPresentation, type ApiError } from '@/api/errors'
import { messages } from '@/i18n/index'

function apiError(
  status: number,
  code: string,
  fields: ApiError['fields'] = [],
  currentVersion: ApiError['currentVersion'] = null,
): ApiError {
  return {
    status,
    code: code as ApiError['code'],
    message: 'x',
    fields,
    requestId: 'req-1',
    currentVersion,
  }
}

describe('errorPresentation (ui.md §4)', () => {
  it.each([
    { code: 'campaign_not_found', status: 404, display: 'page' },
    { code: 'not_found', status: 404, display: 'page' },
    { code: 'method_not_allowed', status: 405, display: 'toast', variant: 'error' },
    { code: 'content_version_not_found', status: 404, display: 'toast', variant: 'error' },
    { code: 'customer_not_found', status: 404, display: 'toast', variant: 'warning' },
    { code: 'invalid_state', status: 409, display: 'toast', variant: 'warning' },
    { code: 'content_not_validated', status: 409, display: 'toast', variant: 'warning' },
    { code: 'stale_content_version', status: 409, display: 'toast', variant: 'warning' },
    { code: 'generation_in_progress', status: 409, display: 'toast', variant: 'warning' },
    { code: 'payload_too_large', status: 413, display: 'inline' },
    { code: 'validation_failed', status: 422, display: 'inline' },
    { code: 'llm_invalid_output', status: 502, display: 'card' },
    { code: 'llm_unavailable', status: 502, display: 'card' },
    { code: 'malformed_request', status: 400, display: 'toast', variant: 'error' },
    { code: 'internal_error', status: 500, display: 'toast', variant: 'error' },
  ])('$code ($status) => error.$code, $display', ({ code, status, display, variant }) => {
    const p = errorPresentation(apiError(status, code))
    expect(p.messageId).toBe(`error.${code}`)
    expect(p.display).toBe(display)
    if (variant) expect(p.variant).toBe(variant)
  })

  // REQ-APR-03 (D40): an edit conflict is shown inline in the editor and carries the current version.
  it('content_edit_conflict (409) => error.content_edit_conflict, inline, currentVersion kept', () => {
    const e = apiError(409, 'content_edit_conflict', [], {
      id: '5c1d7e3a-2b4f-4c6d-8e9f-0a1b2c3d4e5f',
      version_number: 4,
    })
    const p = errorPresentation(e, 'content')
    expect(p.messageId).toBe('error.content_edit_conflict')
    expect(p.display).toBe('inline')
    expect(e.currentVersion).toEqual({
      id: '5c1d7e3a-2b4f-4c6d-8e9f-0a1b2c3d4e5f',
      version_number: 4,
    })
  })

  it('stale_content_version keeps its message id in the approval context', () => {
    const p = errorPresentation(apiError(409, 'stale_content_version'), 'approval')
    expect(p.messageId).toBe('error.stale_content_version')
  })

  it('network_error => error.network shown as a banner', () => {
    const p = errorPresentation(apiError(0, 'network_error'))
    expect(p.messageId).toBe('error.network')
    expect(p.display).toBe('banner')
  })

  it.each(['brief', 'audience'] as const)(
    'brief errors keep their message id in context %s',
    (ctx) => {
      const e = apiError(422, 'validation_failed', [
        { path: 'brief', code: 'too_long', message: 'x', limit: 2000, actual: 2001 },
      ])
      expect(errorPresentation(e, ctx).messageId).toBe('error.validation_failed')
      expect(errorPresentation(apiError(413, 'payload_too_large'), ctx).messageId).toBe(
        'error.payload_too_large',
      )
      expect(errorPresentation(apiError(502, 'llm_invalid_output'), ctx).messageId).toBe(
        'error.llm_invalid_output',
      )
    },
  )
})

const ERROR_CODES = [
  'malformed_request',
  'not_found',
  'method_not_allowed',
  'campaign_not_found',
  'content_version_not_found',
  'customer_not_found',
  'invalid_state',
  'content_not_validated',
  'stale_content_version',
  'content_edit_conflict',
  'generation_in_progress',
  'payload_too_large',
  'validation_failed',
  'llm_invalid_output',
  'llm_unavailable',
  'internal_error',
]

const FIELD_CODES = [
  'required',
  'too_long',
  'invalid_value',
  'invalid_json',
  'schema_violation',
  'truncated_output',
  'no_tool_call',
  'llm_unavailable',
  'internal_error',
  'sms_too_long',
  'subject_too_long',
  'email_body_too_long',
  'sms_too_long_worst_case',
  'subject_too_long_worst_case',
  'unknown_variable',
  'malformed_variable',
  'unknown_field',
  'unknown_operator',
  'operator_not_allowed',
  'type_mismatch',
  'empty_group',
  'max_depth_exceeded',
  'max_conditions_exceeded',
]

const OTHER_IDS = [
  'provider.mock',
  'provider.claude',
  'provider.unknown',
  'status.content.draft',
  'status.content.validated',
  'status.content.invalid',
  'status.content.interrupted',
  'status.delivery.queued',
  'status.delivery.sending',
  'status.delivery.sent',
  'status.delivery.failed',
  'status.campaign.draft',
  'status.campaign.audience_ready',
  'status.campaign.content_ready',
  'status.campaign.approved',
  'status.campaign.sending',
  'status.campaign.completed',
  'step.brief',
  'step.audience',
  'step.content',
  'step.approval',
  'step.tracking',
  'audience.consentNote',
  'audience.empty',
  'counter.worstCase',
  'stream.retrying',
  'stream.interrupted',
  'approval.reason.no_content',
  'approval.reason.streaming',
  'approval.reason.invalid',
  'approval.reason.interrupted',
  'approval.reason.dirty',
  'approval.reason.state',
  'approval.staleNote',
  'confirm.approve.title',
  'confirm.send.title',
  'confirm.regenerate.title',
  'preview.emailFrameTitle',
  'preview.fallbackRecipient',
  'preview.sample',
  'feed.empty',
  'feed.newItems',
  'connection.lost',
  'delivery.staleNote',
  'delivery.completedTitle',
  'error.network',
  'field_error.unknown',
  // P4 (ui.md §4 "Diğer zorunlu MessageId'ler")
  'editor.conflict.title',
  'editor.conflict.reload',
  'editor.conflict.keepMine',
  'editor.conflict.keepMineWarning',
  'tabs.channels',
  'content.generate',
  'content.regenerate',
  'content.editDraft',
  'content.empty',
  'content.live.streaming',
  'content.live.retrying',
  'content.live.validated',
  'content.live.invalid',
  'content.live.interrupted',
  'approval.version',
  'approval.edit',
  'toast.contentSaved',
  'toast.approved',
]

describe('i18n message ids (ui.md §4)', () => {
  const required = [
    ...ERROR_CODES.map((c) => `error.${c}`),
    ...FIELD_CODES.map((c) => `field_error.${c}`),
    ...OTHER_IDS,
  ]

  it.each(required)('%s has a non-empty message', (id) => {
    const table = messages as Record<string, string>
    expect(Object.prototype.hasOwnProperty.call(table, id)).toBe(true)
    const value = table[id]
    expect(typeof value).toBe('string')
    // `?? ''` only narrows the type: a missing id already failed the line above and '' fails below.
    expect((value ?? '').trim()).not.toBe('')
  })
})
