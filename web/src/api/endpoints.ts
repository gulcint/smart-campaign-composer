// Typed wrappers for the openapi.yaml operations used by the UI.
import type { ApiClient } from './client'
import type { AppConfig, Campaign, CreateCampaignRequest, SegmentResult } from './types'

const campaignPath = (id: string) => `/api/campaigns/${encodeURIComponent(id)}`

export const api = {
  /** getConfig — GET /api/config (REQ-AI-03) */
  getConfig: (c: ApiClient) => c.request<AppConfig>('GET', '/api/config'),

  /** createCampaign — POST /api/campaigns (REQ-AUD-05) */
  createCampaign: (c: ApiClient, body: CreateCampaignRequest) =>
    c.request<Campaign>('POST', '/api/campaigns', { body }),

  /** getCampaign — GET /api/campaigns/{campaign_id} */
  getCampaign: (c: ApiClient, id: string) => c.request<Campaign>('GET', campaignPath(id)),

  /** generateSegment — POST /api/campaigns/{campaign_id}/segment (REQ-AUD-00..04) */
  generateSegment: (c: ApiClient, id: string) =>
    c.request<SegmentResult>('POST', `${campaignPath(id)}/segment`, { body: {} }),
}
