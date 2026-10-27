<script setup lang="ts">
import { useRouter } from 'vue-router'

import EmptyState from '@/components/EmptyState.vue'
import { t } from '@/i18n'

const router = useRouter()
</script>

<template>
  <div data-testid="page-not-found" class="mx-auto flex w-full max-w-3xl flex-col gap-4">
    <h1 data-testid="page-title" tabindex="-1" class="sr-only">{{ t('empty.not_found.title') }}</h1>
    <EmptyState kind="not_found" @action="router.push({ name: 'brief-new' })" />
  </div>
</template>
