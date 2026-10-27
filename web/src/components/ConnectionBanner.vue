<script setup lang="ts">
// ui.md §3.22: sticky warning strip shown after a network error.
import { WifiOff } from '@lucide/vue'

import { t } from '@/i18n'

defineProps<{ visible: boolean }>()
</script>

<template>
  <div
    v-if="visible"
    data-testid="connection-banner"
    role="status"
    class="sticky top-16 z-20 flex items-center justify-center gap-2 bg-warning-bg px-4 py-2 text-body-sm font-medium text-warning"
  >
    <WifiOff class="size-4" aria-hidden="true" />
    <span>{{ t('connection.lost') }}</span>
  </div>
</template>
