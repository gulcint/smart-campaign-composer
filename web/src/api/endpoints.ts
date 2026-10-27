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
}
