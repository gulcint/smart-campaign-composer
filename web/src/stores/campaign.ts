// Current campaign: create (POST /api/campaigns), load (GET), generate segment
// (POST /segment). The server is the single source of truth; no optimistic updates.
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api } from '@/api/endpoints'
import type { ApiError } from '@/api/errors'
import { toApiError } from '@/api/errors'
import type { Campaign, FieldError } from '@/api/types'

import { injectApiClient, reportError } from './api'
import { useUiStore } from './ui'

export const useCampaignStore = defineStore('campaign', () => {
  const client = injectApiClient()

  const campaign = ref<Campaign | null>(null)
  const loading = ref(false)
  /** GET error for `loadErrorId` (404 => EmptyState campaign_not_found). */
  const loadError = ref<ApiError | null>(null)
  const loadErrorId = ref<string | null>(null)

  const creating = ref(false)
  /** 413/422 on create, as brief field errors (ui.md §4). */
  const createFieldErrors = ref<FieldError[]>([])
  /** create errors that are not field errors and not toast/banner (unmapped 422 fields). */
  const createError = ref<ApiError | null>(null)

  const segmentGenerating = ref(false)
  /** Last segment error, tagged with its campaign (ui.md §1.1: ErrorCard action="retry"). */
  const segmentError = ref<ApiError | null>(null)
  const segmentErrorId = ref<string | null>(null)

  /** Brief text carried into a new campaign by "Brief'i düzenle" (ui.md §1.1). */
  const prefillBrief = ref('')

  const state = computed(() => campaign.value?.state ?? null)

  function ok() {
    useUiStore().connectionLost = false
  }

  function isNotFound(id: string) {
    return loadErrorId.value === id && loadError.value?.status === 404
  }

  function segmentErrorFor(id: string) {
    return segmentErrorId.value === id ? segmentError.value : null
  }

  async function load(id: string): Promise<Campaign | null> {
    loading.value = true
    try {
      const c = await api.getCampaign(client, id)
      campaign.value = c
      loadError.value = null
      loadErrorId.value = null
      ok()
      return c
    } catch (e) {
      const err = toApiError(e)
      if (campaign.value?.id === id) campaign.value = null
      loadError.value = err
      loadErrorId.value = id
      if (err.status !== 404) reportError(err)
      return null
    } finally {
      loading.value = false
    }
  }

  /** Loads the campaign unless it is already the current one. */
  async function ensure(id: string): Promise<Campaign | null> {
    if (campaign.value?.id === id) return campaign.value
    if (isNotFound(id)) return null
    return load(id)
  }

  async function create(brief: string): Promise<Campaign | null> {
    creating.value = true
    createFieldErrors.value = []
    createError.value = null
    try {
      const c = await api.createCampaign(client, { brief })
      campaign.value = c
      ok()
      return c
    } catch (e) {
      const err = toApiError(e)
      if (err.code === 'payload_too_large') {
        createFieldErrors.value = [{ path: 'brief', code: 'payload_too_large', message: '' }]
      } else if (err.code === 'validation_failed') {
        createFieldErrors.value = err.fields.filter((f) => f.path === 'brief')
        if (createFieldErrors.value.length !== err.fields.length) createError.value = err
      } else if (!reportError(err, 'brief')) {
        createError.value = err
      }
      return null
    } finally {
      creating.value = false
    }
  }

  /** Returns true on success; on failure the error is kept for the ErrorCard. */
  async function generateSegment(id: string): Promise<boolean> {
    segmentGenerating.value = true
    segmentError.value = null
    segmentErrorId.value = null
    try {
      await api.generateSegment(client, id)
      ok()
      // SegmentResult carries state + segment; re-read the campaign so every field
      // (latest_content_version reset, approval revoked) matches the server.
      await load(id)
      return true
    } catch (e) {
      const err = toApiError(e)
      if (err.code === 'network_error') reportError(err)
      segmentError.value = err
      segmentErrorId.value = id
      if (err.code === 'invalid_state' || err.code === 'campaign_not_found') await load(id)
      return false
    } finally {
      segmentGenerating.value = false
    }
  }

  function clearCreateErrors() {
    createFieldErrors.value = []
    createError.value = null
  }

  return {
    campaign,
    state,
    loading,
    loadError,
    creating,
    createFieldErrors,
    createError,
    segmentGenerating,
    segmentError,
    segmentErrorId,
    loadErrorId,
    prefillBrief,
    isNotFound,
    segmentErrorFor,
    load,
    ensure,
    create,
    generateSegment,
    clearCreateErrors,
  }
})
