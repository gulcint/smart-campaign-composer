<script setup lang="ts">
// ui.md §3.22 / DESIGN.md §7: icon, title, description and one action.
import { Inbox, SearchX, Users } from '@lucide/vue'
import { computed } from 'vue'

import { t } from '@/i18n'

import Button from './Button.vue'

const props = defineProps<{
  kind: 'campaign_not_found' | 'not_found' | 'audience_empty' | 'feed_empty'
}>()
const emit = defineEmits<{ action: [] }>()

const icon = computed(() =>
  props.kind === 'audience_empty' ? Users : props.kind === 'feed_empty' ? Inbox : SearchX,
)
const hasAction = computed(() => props.kind === 'campaign_not_found' || props.kind === 'not_found')
</script>

<template>
  <div
    data-testid="empty-state"
    :data-kind="kind"
    class="flex flex-col items-center gap-3 px-4 py-12 text-center"
  >
    <component :is="icon" class="size-12 stroke-[1.5] text-brand-icon" aria-hidden="true" />
    <p class="text-h3 text-accent-heading">{{ t(`empty.${kind}.title`) }}</p>
    <p class="max-w-md text-body text-ink-600">{{ t(`empty.${kind}.body`) }}</p>
    <Button
      v-if="hasAction"
      data-testid="empty-state-action"
      variant="secondary"
      @click="emit('action')"
    >
      {{ t('empty.newCampaign') }}
    </Button>
  </div>
</template>
