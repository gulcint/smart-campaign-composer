// GET /api/config once at start-up; feeds ProviderBadge (ui.md §2, REQ-AI-03).
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api } from '@/api/endpoints'
import { toApiError } from '@/api/errors'
import type { AppConfig } from '@/api/types'

import { injectApiClient, reportError } from './api'
import { useUiStore } from './ui'

export const useConfigStore = defineStore('config', () => {
  const client = injectApiClient()
  const config = ref<AppConfig | null>(null)
  const loading = ref(false)
  let started = false

  const provider = computed(() => config.value?.llm_provider ?? null)

  async function load() {
    if (started) return
    started = true
    loading.value = true
    try {
      config.value = await api.getConfig(client)
      useUiStore().connectionLost = false
    } catch (e) {
      // Badge stays "unknown"; only the network banner is worth showing here.
      const err = toApiError(e)
      if (err.code === 'network_error') reportError(err)
      started = false
    } finally {
      loading.value = false
    }
  }

  return { config, loading, provider, load }
})
