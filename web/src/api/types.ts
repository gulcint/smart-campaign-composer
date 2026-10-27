// TypeScript mirrors of docs/contracts/openapi.yaml components.schemas (same names,
// snake_case fields exactly as on the wire). Keep in sync with the contract.

export type CampaignState =
  'draft' | 'audience_ready' | 'content_ready' | 'approved' | 'sending' | 'completed'

export type Channel = 'sms' | 'email'
export type DeliveryStatus = 'queued' | 'sending' | 'sent' | 'failed'
export type FailureReason =
  | 'provider_rejected'
  | 'max_attempts_exceeded'
  | 'rate_limit_budget_exhausted'
  | 'content_too_long'
  | 'recipient_unavailable'

export type MockScenario =
  | 'ok'
  | 'invalid_json'
  | 'schema_violation'
  | 'over_limit_sms'
  | 'over_limit_subject'
  | 'unknown_variable'
  | 'injection_attempt'
  | 'truncated_stream'
  | 'then_ok_after_repair'
  | 'always_invalid'
  | 'sql_in_value'
  | 'html_in_content'
  | 'llm_unavailable'
  | 'stalled_stream'

export type LlmProvider = 'mock' | 'claude'

export interface AppConfig {
  llm_provider: LlmProvider
  mock_scenarios?: MockScenario[]
  limits: {
    sms_max_runes: number
    email_subject_max_runes: number
    email_body_max_runes: number
    brief_max_runes: number
    request_body_max_bytes: number
  }
  template_variables: string[]
  first_name_fallback: string
}

export interface CreateCampaignRequest {
  brief: string
}

export interface Audience {
  matched: number
  sms: number
  email: number
}

/** Segment rule tree (segment-dsl.md §1); validated by the backend, displayed as data. */
export type SegmentRule = Record<string, unknown>

export interface LongestValues {
  first_name: number
  last_name: number
  city: number
}

export interface Segment {
  rule: SegmentRule
  audience: Audience
  longest_values: LongestValues
  reference_time: string
  revision: number
  llm_attempts: 1 | 2
  generated_at: string
}

export interface WorstCase {
  sms_runes: number
  subject_runes: number
}

export interface SegmentResult {
  campaign_id: string
  state: CampaignState
  segment: Segment
}

export interface SegmentCount {
  audience: Audience
  reference_time: string
}

export interface Content {
  sms: { body: string }
  email: { subject: string; body: string }
}

export type FieldErrorCode =
  | 'required'
  | 'too_long'
  | 'invalid_value'
  | 'invalid_json'
  | 'schema_violation'
  | 'truncated_output'
  | 'no_tool_call'
  | 'llm_unavailable'
  | 'internal_error'
  | 'sms_too_long'
  | 'subject_too_long'
  | 'email_body_too_long'
  | 'sms_too_long_worst_case'
  | 'subject_too_long_worst_case'
  | 'unknown_variable'
  | 'malformed_variable'
  | 'unknown_field'
  | 'unknown_operator'
  | 'operator_not_allowed'
  | 'type_mismatch'
  | 'empty_group'
  | 'max_depth_exceeded'
  | 'max_conditions_exceeded'

export interface FieldError {
  path: string
  /** One of FieldErrorCode on the wire; kept as string so unknown codes still render. */
  code: string
  message: string
  limit?: number
  actual?: number
}

export interface ContentVersion {
  id: string
  campaign_id: string
  version_number: number
  segment_revision: number
  status: 'validated' | 'invalid'
  source: 'llm' | 'edit'
  content: Content
  worst_case: WorstCase | null
  validation_errors: FieldError[]
  llm_attempts: 1 | 2 | null
  created_at: string
}

export interface Approval {
  id: string
  content_version_id: string
  approved_at: string
}

export interface Campaign {
  id: string
  brief: string
  state: CampaignState
  segment: Segment | null
  latest_content_version: ContentVersion | null
  approval: Approval | null
  sending_started_at: string | null
  completed_at: string | null
  created_at: string
  updated_at: string
}

export interface CampaignPage {
  items: Campaign[]
  total: number
  limit: number
  offset: number
}

export interface VersionRef {
  content_version_id: string
}

export interface SendResult {
  campaign_id: string
  state: 'sending' | 'completed'
  content_version_id: string
  deliveries: { total: number; sms: number; email: number }
}

export interface StatusCounts {
  total: number
  queued: number
  sending: number
  sent: number
  failed: number
}

export interface CampaignStatus {
  campaign_id: string
  state: CampaignState
  counts: StatusCounts
  by_channel: { sms: StatusCounts; email: StatusCounts }
  sending_started_at: string | null
  completed_at: string | null
  updated_at: string
}

export interface Delivery {
  id: string
  campaign_id: string
  content_version_id: string
  customer_id: string
  channel: Channel
  status: DeliveryStatus
  attempts: number
  failed_attempts: number
  rate_limited_count: number
  retry_count: number
  next_attempt_at: string
  first_attempt_at: string | null
  last_attempt_at: string | null
  last_http_status: number | null
  last_error: string | null
  failure_reason: FailureReason | null
  provider_message_id: string | null
  sent_at: string | null
  failed_at: string | null
  created_at: string
  updated_at: string
}

export interface DeliveryPage {
  items: Delivery[]
  total: number
  limit: number
  offset: number
}

export interface Preview {
  campaign_id: string
  content_version_id: string
  sample_index: number
  sample_count: number
  customer: { id: string; first_name: string; last_name: string; city: string }
  sms: { text: string; runes: number; limit: number; within_limit: boolean }
  email: {
    subject: string
    subject_runes: number
    subject_limit: number
    within_limit: boolean
    text: string
    html: string
  }
}

export type ErrorCode =
  | 'malformed_request'
  | 'not_found'
  | 'method_not_allowed'
  | 'campaign_not_found'
  | 'content_version_not_found'
  | 'customer_not_found'
  | 'invalid_state'
  | 'content_not_validated'
  | 'stale_content_version'
  | 'generation_in_progress'
  | 'payload_too_large'
  | 'validation_failed'
  | 'llm_invalid_output'
  | 'llm_unavailable'
  | 'internal_error'

export interface ErrorResponse {
  error: {
    code: ErrorCode
    message: string
    fields?: FieldError[]
    request_id: string
  }
}

// --- UI-side shared types (docs/contracts/ui.md §0) ---

export type ChannelKey = Channel
export type FieldKey = 'sms-body' | 'email-subject' | 'email-body'
export type ContentPhase =
  'idle' | 'streaming' | 'retrying' | 'validated' | 'invalid' | 'interrupted'
export type VersionStatus = 'none' | 'streaming' | 'validated' | 'invalid' | 'interrupted'
export type StepId = 'brief' | 'audience' | 'content' | 'approval' | 'tracking'
export type ConnectionState = 'idle' | 'connecting' | 'open' | 'closed' | 'error'
