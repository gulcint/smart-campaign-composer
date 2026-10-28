<script setup lang="ts">
// ui.md §3.8a: rule area while no segment exists yet. Real heading + an aria-hidden shimmer
// chips row with SegmentRuleView's geometry (30 px chips, 32/44 px JSON toggle, same wrapping).
import { ListFilter } from '@lucide/vue'

import { t } from '@/i18n'

import Skeleton from './Skeleton.vue'
</script>

<template>
  <div class="flex flex-col gap-2">
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
</template>
