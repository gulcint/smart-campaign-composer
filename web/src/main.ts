import '@fontsource-variable/figtree'
import '@fontsource-variable/jetbrains-mono'
import './style.css'

import { createPinia } from 'pinia'
import { createApp } from 'vue'

import type { EventSourceLike } from '@/api/client'
import { ApiClientKey, createApiClient, EventSourceFactoryKey, FetchKey } from '@/api/client'
import { createAppRouter } from '@/router'

import App from './App.vue'

const app = createApp(App)
// Same origin: nginx (container) or the Vite dev proxy forwards /api to the Go API.
app.provide(ApiClientKey, createApiClient({ baseUrl: '' }))
app.provide(FetchKey, (...args: Parameters<typeof fetch>) => globalThis.fetch(...args))
// The DOM EventSource satisfies EventSourceLike at run time; its readyState is typed `number`.
app.provide(
  EventSourceFactoryKey,
  (url: string) => new EventSource(url) as unknown as EventSourceLike,
)
app.use(createPinia())
app.use(createAppRouter())
app.mount('#app')
