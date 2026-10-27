<script setup lang="ts">
// ui.md §3.22: text comes only from i18n (`field_error.<code>`), never from the API message.
import { CircleAlert } from '@lucide/vue'
import { computed } from 'vue'

import type { MessageId } from '@/i18n'
import { hasMessage, t } from '@/i18n'

const props = withDefaults(
  defineProps<{
    path: string
    code: string
    limit?: number | null
    actual?: number | null
    /** false when a parent wrapper owns the `inline-error-<pathKey>` id (ui.md §3.14 e). */
    withId?: boolean
  }>(),
  { limit: null, actual: null, withId: true },
)

/** API path -> testid suffix (ui.md §0): sms.body -> sms-body, … */
const pathKey = computed(() => props.path.replace(/\./g, '-'))

const text = computed(() => {
  const id = `field_error.${props.code}`
  const params = { limit: props.limit ?? '', actual: props.actual ?? '' }
  return hasMessage(id) ? t(id as MessageId, params) : t('field_error.unknown', params)
})
</script>

<template>
  <p
    :id="withId ? `inline-error-${pathKey}` : undefined"
    :data-testid="`inline-error-${pathKey}`"
    :data-code="code"
    role="alert"
    class="flex items-start gap-1.5 text-body-sm text-error"
  >
    <CircleAlert class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
    <span>{{ text }}</span>
  </p>
</template>
