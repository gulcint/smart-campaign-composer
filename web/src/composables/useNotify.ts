// Toast queue (ui.md §3.22): `error` toasts stay until closed, the rest close after 5 s.
import { reactive, readonly } from 'vue'

import type { MessageId } from '@/i18n'

export type ToastVariant = 'success' | 'info' | 'warning' | 'error'

export interface Toast {
  id: number
  variant: ToastVariant
  messageId: MessageId
  params?: Record<string, string | number>
}

export const TOAST_TIMEOUT_MS = 5000

const toasts = reactive<Toast[]>([])
let nextId = 1

function dismiss(id: number) {
  const i = toasts.findIndex((x) => x.id === id)
  if (i !== -1) toasts.splice(i, 1)
}

function notify(opts: {
  variant: ToastVariant
  messageId: MessageId
  params?: Record<string, string | number>
}): number {
  const id = nextId++
  toasts.push({ id, ...opts })
  if (opts.variant !== 'error') setTimeout(() => dismiss(id), TOAST_TIMEOUT_MS)
  return id
}

export function useNotify() {
  return { toasts: readonly(toasts), notify, dismiss }
}
