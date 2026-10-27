// Thin aliases over the types generated from docs/contracts/openapi.yaml (schema.gen.ts,
// `make api-types`). Names match the OpenAPI schema names used across the UI and ui.md §0;
// the wire shapes themselves come only from the spec, so they cannot drift from it.
import type { components } from './schema.gen'

type Schemas = components['schemas']

export type CampaignState = Schemas['CampaignState']
export type Channel = Schemas['Channel']
export type DeliveryStatus = Schemas['DeliveryStatus']
export type FailureReason = Schemas['FailureReason']
export type MockScenario = Schemas['MockScenario']

export type AppConfig = Schemas['AppConfig']
export type LlmProvider = AppConfig['llm_provider']

export type CreateCampaignRequest = Schemas['CreateCampaignRequest']
export type Audience = Schemas['Audience']

/** Segment rule tree (segment-dsl.md §1, schemas/segment-rule.schema.json): the root group. */
export type SegmentRule = Schemas['group1']
export type SegmentCondition = Schemas['condition']
/**
 * Body of POST /api/segments/count. Picked from the generated type because openapi-typescript
 * also emits the JSON Schema `$defs` keyword as a (non-wire) property.
 */
export type SegmentRuleDocument = Pick<Schemas['SegmentRuleDocument'], 'rule'>

export type LongestValues = Schemas['LongestValues']
export type Segment = Schemas['Segment']
export type WorstCase = Schemas['WorstCase']
export type SegmentResult = Schemas['SegmentResult']
export type SegmentCount = Schemas['SegmentCount']

export type Content = Schemas['Content']
export type ContentDraft = Schemas['ContentDraft']
export type ContentVersion = Schemas['ContentVersion']
export type Approval = Schemas['Approval']
export type Campaign = Schemas['Campaign']
export type CampaignPage = Schemas['CampaignPage']
export type VersionRef = Schemas['VersionRef']
export type SendResult = Schemas['SendResult']
export type StatusCounts = Schemas['StatusCounts']
export type CampaignStatus = Schemas['CampaignStatus']
export type Delivery = Schemas['Delivery']
export type DeliveryPage = Schemas['DeliveryPage']
export type Preview = Schemas['Preview']

// Content stream event payloads (sse-events.md §1.1; openapi components of the same name).
export type ContentDeltaEvent = Schemas['ContentDeltaEvent']
export type ContentInvalidEvent = Schemas['ContentInvalidEvent']
export type ContentValidatedEvent = Schemas['ContentValidatedEvent']

export type FieldErrorCode = Schemas['FieldError']['code']
/**
 * Field error as the UI handles it: the spec's FieldError with `code` widened to string, so
 * client-side codes (413 `payload_too_large` on the brief) and codes from a newer server still
 * render (InlineError falls back to `field_error.unknown`).
 */
export type FieldError = Omit<Schemas['FieldError'], 'code'> & {
  code: FieldErrorCode | (string & {})
}

export type ErrorResponse = Schemas['ErrorResponse']
export type ErrorCode = ErrorResponse['error']['code']

// --- UI-side shared types (docs/contracts/ui.md §0) ---

export type ChannelKey = Channel
export type FieldKey = 'sms-body' | 'email-subject' | 'email-body'
export type ContentPhase =
  'idle' | 'streaming' | 'retrying' | 'validated' | 'invalid' | 'interrupted'
export type VersionStatus = 'none' | 'streaming' | 'validated' | 'invalid' | 'interrupted'
export type StepId = 'brief' | 'audience' | 'content' | 'approval' | 'tracking'
export type ConnectionState = 'idle' | 'connecting' | 'open' | 'closed' | 'error'
