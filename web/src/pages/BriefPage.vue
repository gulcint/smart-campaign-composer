<script setup lang="ts">
// Step 1 (ui.md §1, §1.1; DESIGN.md §8.1). `brief-new`: compose and submit.
// `brief`: the stored brief, read-only, plus the segment error card when segment generation failed.
import { Info, Sparkles } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import BriefComposer from '@/components/BriefComposer.vue'
import Button from '@/components/Button.vue'
import ChatBubble from '@/components/ChatBubble.vue'
import EmptyState from '@/components/EmptyState.vue'
import ErrorCard from '@/components/ErrorCard.vue'
import Skeleton from '@/components/Skeleton.vue'
import { t } from '@/i18n'
import { campaignIdOf } from '@/router'
import { useCampaignStore } from '@/stores/campaign'

const route = useRoute()
const router = useRouter()
const store = useCampaignStore()

const campaignId = computed(() => (route ? campaignIdOf(route) : null))
const isNew = computed(() => !campaignId.value)

// --- brief-new ---
const draft = ref('')
const submittedText = ref('')

onMounted(() => {
  if (store.prefillBrief) {
    draft.value = store.prefillBrief
    store.prefillBrief = ''
  }
  if (campaignId.value) void store.ensure(campaignId.value)
})

watch(draft, () => {
  if (store.createFieldErrors.length || store.createError) store.clearCreateErrors()
})

const submitting = computed(() => store.creating || store.segmentGenerating)

async function onSubmit(brief: string) {
  submittedText.value = brief
  const created = await store.create(brief)
  if (!created) return
  const ok = await store.generateSegment(created.id)
  const name = ok ? 'audience' : 'brief'
  await router.push({ name, params: { campaignId: created.id } })
}

// --- brief (read-only) ---
const campaign = computed(() =>
  campaignId.value && store.campaign?.id === campaignId.value ? store.campaign : null,
)
const notFound = computed(() => (campaignId.value ? store.isNotFound(campaignId.value) : false))
const loadError = computed(() =>
  campaignId.value && store.loadErrorId === campaignId.value && !notFound.value
    ? store.loadError
    : null,
)
const segmentError = computed(() =>
  campaignId.value ? store.segmentErrorFor(campaignId.value) : null,
)

async function retrySegment() {
  const id = campaignId.value
  if (!id) return
  if (await store.generateSegment(id))
    await router.push({ name: 'audience', params: { campaignId: id } })
}

function newCampaign() {
  void router.push({ name: 'brief-new' })
}
</script>

<template>
  <div data-testid="page-brief" class="mx-auto flex w-full max-w-3xl flex-col gap-6">
    <template v-if="isNew">
      <header class="flex flex-col gap-3 pt-4 md:pt-10">
        <span
          class="flex size-10 items-center justify-center rounded-bubble bg-gradient-brand text-primary-foreground"
          aria-hidden="true"
        >
          <Sparkles class="size-5" />
        </span>
        <h1 data-testid="page-title" tabindex="-1" class="text-display text-ink-900 outline-none">
          {{ t('brief.titleLead') }}
          <span class="block text-accent-display">{{ t('brief.titleAccent') }}</span>
        </h1>
        <p class="text-body-lg text-ink-600">{{ t('brief.subtitle') }}</p>
      </header>

      <template v-if="submitting && submittedText">
        <ChatBubble role="user" :text="submittedText" />
        <ChatBubble role="assistant" :text="t('brief.pending')" pending />
      </template>

      <BriefComposer
        v-model="draft"
        :submitting="submitting"
        :field-errors="store.createFieldErrors"
        @submit="onSubmit"
        @use-example="draft = t('brief.exampleText')"
      />

      <ErrorCard
        v-if="store.createError"
        :error="store.createError"
        :action="null"
        context="brief"
      />

      <p class="flex items-start gap-2 text-body-sm text-ink-500">
        <Info class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        {{ t('app.seedNote') }}
      </p>
    </template>

    <template v-else>
      <EmptyState v-if="notFound" kind="campaign_not_found" @action="newCampaign" />
      <template v-else>
        <h1 data-testid="page-title" tabindex="-1" class="pt-4 text-h1 outline-none">
          {{ t('brief.readonlyTitle') }}
        </h1>
        <ErrorCard
          v-if="loadError"
          :error="loadError"
          action="retry"
          context="brief"
          @action="campaignId && store.load(campaignId)"
        />
        <Skeleton v-else-if="!campaign" class="h-16 w-full rounded-bubble" />
        <template v-else>
          <ChatBubble role="user" :text="campaign.brief" />
          <ChatBubble
            v-if="store.segmentGenerating"
            role="assistant"
            :text="t('brief.pending')"
            pending
          />
          <ErrorCard
            v-else-if="segmentError"
            :error="segmentError"
            action="retry"
            context="brief"
            @action="retrySegment"
          />
          <div
            v-else-if="campaign.state === 'draft'"
            class="flex flex-col gap-3 rounded-card border border-border-default bg-surface-card p-4 shadow-card sm:flex-row sm:items-center sm:justify-between"
          >
            <p class="text-body-sm text-ink-600">{{ t('brief.draftNote') }}</p>
            <Button data-testid="brief-retry-segment" @click="retrySegment">
              {{ t('brief.retrySegment') }}
            </Button>
          </div>
        </template>
      </template>
    </template>
  </div>
</template>
