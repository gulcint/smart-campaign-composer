<script setup lang="ts">
// Shared body of the steps that land in later phases (content P4, approval P4, tracking P5):
// the route, page root testid and focusable title exist now (ui.md §1).
import { Construction } from '@lucide/vue'
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { MessageId } from '@/i18n'
import { t } from '@/i18n'
import { campaignIdOf } from '@/router'
import { useCampaignStore } from '@/stores/campaign'

import EmptyState from './EmptyState.vue'

const props = defineProps<{ titleId: MessageId }>()

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()
const campaignId = computed(() => (route ? campaignIdOf(route) : null))
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))

onMounted(() => {
  if (campaignId.value) void store.ensure(campaignId.value)
})
</script>

<template>
  <EmptyState
    v-if="notFound"
    kind="campaign_not_found"
    @action="router.push({ name: 'brief-new' })"
  />
  <template v-else>
    <h1 data-testid="page-title" tabindex="-1" class="pt-4 text-h1 outline-none">
      {{ t(props.titleId) }}
    </h1>
    <p class="flex items-center gap-2 text-body text-ink-600">
      <Construction class="size-5 text-brand-icon" aria-hidden="true" />
      {{ t('page.placeholder') }}
    </p>
  </template>
</template>
