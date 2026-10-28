<script setup lang="ts">
// Step 2 (ui.md §1.1, §3.7, §3.8; DESIGN.md §8.2).
import { ArrowUpRight, ListFilter, PencilLine } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AudienceSummaryCard from '@/components/AudienceSummaryCard.vue'
import Button from '@/components/Button.vue'
import ChatBubble from '@/components/ChatBubble.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import SegmentRuleView from '@/components/SegmentRuleView.vue'
import Skeleton from '@/components/Skeleton.vue'
import { t } from '@/i18n'
import { isLockedForEditing } from '@/lib/steps'
import { campaignIdOf } from '@/router'
import { useCampaignStore } from '@/stores/campaign'
import { useConfigStore } from '@/stores/config'

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()
const config = useConfigStore()

const campaignId = computed(() => (route ? campaignIdOf(route) : null))
const campaign = computed(() =>
  campaignId.value && store.campaign?.id === campaignId.value ? store.campaign : null,
)
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))
const segment = computed(() => campaign.value?.segment ?? null)
const segmentError = computed(() =>
  campaignId.value ? store.segmentErrorFor(campaignId.value) : null,
)
const loadError = computed(() =>
  campaignId.value && store.loadErrorId === campaignId.value && !notFound.value
    ? store.loadError
    : null,
)
const cardError = computed(() => segmentError.value ?? loadError.value)
const loading = computed(
  () => store.segmentGenerating || (!campaign.value && !cardError.value) || store.loading,
)

const canGenerate = computed(() => {
  const c = campaign.value
  if (!c || !c.segment) return false
  const stateOk =
    c.state === 'audience_ready' || c.state === 'content_ready' || c.state === 'approved'
  return stateOk && c.segment.audience.matched > 0 && !store.segmentGenerating
})
const canEditBrief = computed(() => !isLockedForEditing(campaign.value?.state ?? null))

onMounted(() => {
  if (campaignId.value) void store.ensure(campaignId.value)
})

const confirmOpen = ref(false)

function goToContent() {
  const id = campaignId.value
  if (!id) return
  void router.push({ name: 'content', params: { campaignId: id }, query: { generate: '1' } })
}

function onGenerate() {
  if (!canGenerate.value) return
  if (campaign.value?.latest_content_version) confirmOpen.value = true
  else goToContent()
}

function onConfirmRegenerate() {
  confirmOpen.value = false
  goToContent()
}

function onEditBrief() {
  if (!canEditBrief.value) return
  store.prefillBrief = campaign.value?.brief ?? ''
  void router.push({ name: 'brief-new' })
}

async function onRetry() {
  const id = campaignId.value
  if (!id) return
  if (segmentError.value) await store.generateSegment(id)
  else await store.load(id)
}
</script>

<template>
  <div data-testid="page-audience" class="mx-auto flex w-full max-w-3xl flex-col gap-6">
    <EmptyState
      v-if="notFound"
      kind="campaign_not_found"
      @action="router.push({ name: 'brief-new' })"
    />
    <template v-else>
      <header class="flex flex-col gap-1 pt-4">
        <h1 data-testid="page-title" tabindex="-1" class="text-h1 outline-none">
          {{ t('audience.title') }}
        </h1>
        <p class="text-body text-ink-600">{{ t('audience.subtitle') }}</p>
      </header>

      <ChatBubble v-if="campaign" role="user" :text="campaign.brief" />

      <section
        class="flex flex-col gap-5 rounded-card border border-border-default bg-surface-card p-4 shadow-card md:p-6"
        :aria-label="t('audience.title')"
      >
        <div v-if="segment" class="flex flex-col gap-2">
          <h2 class="text-h4 text-accent-heading">{{ t('audience.ruleTitle') }}</h2>
          <SegmentRuleView :rule="segment.rule" />
        </div>
        <!-- No rule yet: a shimmer chips row with SegmentRuleView's geometry (30 px chips,
             32/44 px JSON toggle) so the section reads as loading, not empty. -->
        <div v-else-if="!cardError" data-testid="segment-rule-loading" class="flex flex-col gap-2">
          <h2 class="text-h4 text-accent-heading">{{ t('audience.ruleTitle') }}</h2>
          <div class="flex flex-wrap items-center gap-2" aria-hidden="true">
            <ListFilter class="size-4 text-ink-500" />
            <div class="flex flex-wrap gap-2">
              <Skeleton
                v-for="w in ['w-32', 'w-56', 'w-48']"
                :key="w"
                variant="shimmer"
                :class="['h-[30px]', w]"
              />
            </div>
            <Skeleton variant="shimmer" class="ml-auto h-8 w-28 max-lg:h-11" />
          </div>
        </div>
        <div v-if="segment || !cardError" class="h-px bg-border-subtle" aria-hidden="true" />
        <AudienceSummaryCard
          :audience="segment?.audience ?? null"
          :loading="loading"
          :error="cardError"
          :provider="store.segmentGenerating ? config.provider : null"
          @retry="onRetry"
          @edit-brief="onEditBrief"
        />
      </section>

      <div class="flex flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Button
          data-testid="audience-edit-brief"
          variant="secondary"
          :disabled="!canEditBrief"
          @click="onEditBrief"
        >
          <PencilLine aria-hidden="true" />
          {{ t('audience.editBrief') }}
        </Button>
        <Button
          data-testid="audience-generate-content"
          size="lg"
          :disabled="!canGenerate"
          @click="onGenerate"
        >
          {{ t('audience.generateContent') }}
          <ArrowUpRight aria-hidden="true" />
        </Button>
      </div>

      <ConfirmDialog
        v-model:open="confirmOpen"
        kind="regenerate"
        @confirm="onConfirmRegenerate"
        @cancel="confirmOpen = false"
      />
    </template>
  </div>
</template>
