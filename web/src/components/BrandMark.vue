<script setup lang="ts">
// DESIGN.md §1 exception / ui.md §3.2a: the one allowed brand image (own dark background, no
// theme inversion). Decorative by default (`alt=""`); `muted` lowers contrast for empty and
// loading states. width/height are always set so the mark never shifts the layout.
import { computed } from 'vue'

import { t } from '@/i18n'

const props = withDefaults(
  defineProps<{
    size?: number
    muted?: boolean
    decorative?: boolean
  }>(),
  { size: 24, muted: false, decorative: true },
)

const px = computed(() => ({ width: `${props.size}px`, height: `${props.size}px` }))
</script>

<template>
  <img
    data-testid="brand-logo"
    src="/brand/insider-one-logo.jpeg"
    :alt="decorative ? '' : t('app.logoAlt')"
    :width="size"
    :height="size"
    :style="px"
    loading="lazy"
    decoding="async"
    draggable="false"
    :class="['shrink-0 rounded-sm select-none', muted && 'opacity-40 grayscale']"
  />
</template>
