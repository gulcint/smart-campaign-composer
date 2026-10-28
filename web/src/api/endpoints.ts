// Typed wrappers for the openapi.yaml operations used by the UI. Request and response types
// are taken from the generated `operations` by operationId + status code, so a spec change
// (renamed operation, different success code or body) breaks the type check here.
import type { ApiClient } from './client'
import type { operations } from './schema.gen'

type Op = keyof operations
type JsonOf<R> = R extends { content: { 'application/json': infer T } } ? T : never
type Ok<O extends Op, S extends keyof operations[O]['responses']> = JsonOf<
  operations[O]['responses'][S]
>
type BodyOf<O extends Op> = JsonOf<NonNullable<operations[O]['requestBody']>>

const campaignPath = (id: string) => `/api/campaigns/${encodeURIComponent(id)}`

export const api = {
  /** getConfig — GET /api/config (REQ-AI-03) */
  getConfig: (c: ApiClient) => c.request<Ok<'getConfig', 200>>('GET', '/api/config'),

  /** createCampaign — POST /api/campaigns (REQ-AUD-05) */
  createCampaign: (c: ApiClient, body: BodyOf<'createCampaign'>) =>
    c.request<Ok<'createCampaign', 201>>('POST', '/api/campaigns', { body }),

  /** getCampaign — GET /api/campaigns/{campaign_id} */
  getCampaign: (c: ApiClient, id: string) =>
    c.request<Ok<'getCampaign', 200>>('GET', campaignPath(id)),

  /** generateSegment — POST /api/campaigns/{campaign_id}/segment (REQ-AUD-00..04) */
  generateSegment: (c: ApiClient, id: string) =>
    c.request<Ok<'generateSegment', 200>>('POST', `${campaignPath(id)}/segment`, {
      body: {} satisfies BodyOf<'generateSegment'>,
    }),

  /** editContent — POST /api/campaigns/{campaign_id}/content-versions (REQ-APR-02, REQ-APR-03, D40) */
  editContent: (c: ApiClient, id: string, body: BodyOf<'editContent'>, expectedVersion: string) =>
    c.request<Ok<'editContent', 201>>('POST', `${campaignPath(id)}/content-versions`, {
      body,
      headers: { 'X-Expected-Version': expectedVersion },
    }),

  /** previewContent — GET /api/campaigns/{campaign_id}/preview (REQ-APR-01, REQ-CNT-03) */
  previewContent: (c: ApiClient, id: string, sampleIndex = 0) =>
    c.request<Ok<'previewContent', 200>>('GET', `${campaignPath(id)}/preview`, {
      query: { sample_index: sampleIndex },
    }),

  /** approveContent — POST /api/campaigns/{campaign_id}/approve (REQ-APR-01) */
  approveContent: (c: ApiClient, id: string, body: BodyOf<'approveContent'>) =>
    c.request<Ok<'approveContent', 200>>('POST', `${campaignPath(id)}/approve`, { body }),

  /** sendCampaign — POST /api/campaigns/{campaign_id}/send (REQ-SND-01) */
  sendCampaign: (c: ApiClient, id: string, body: BodyOf<'sendCampaign'>) =>
    c.request<Ok<'sendCampaign', 202>>('POST', `${campaignPath(id)}/send`, { body }),

  /** listDeliveries — GET /api/campaigns/{campaign_id}/deliveries (REQ-TRK-01 failed list) */
  listDeliveries: (
    c: ApiClient,
    id: string,
    query: NonNullable<operations['listDeliveries']['parameters']['query']>,
  ) =>
    c.request<Ok<'listDeliveries', 200>>('GET', `${campaignPath(id)}/deliveries`, {
      query: query as Record<string, string | number>,
    }),
}
