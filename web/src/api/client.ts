// Thin fetch-based API client (docs/contracts/ui.md §0). Components and stores receive it
// through ApiClientKey so tests can provide a fake.
import type { InjectionKey } from 'vue'

import { apiErrorFromBody, networkError } from './errors'

export interface RequestOptions {
  body?: unknown
  query?: Record<string, string | number>
  headers?: Record<string, string>
}

export interface ApiClient {
  /** Resolves with the parsed JSON body (undefined for 204); non-2xx throws ApiError. */
  request<T>(method: string, path: string, opts?: RequestOptions): Promise<T>
}

export interface EventSourceLike {
  readonly readyState: 0 | 1 | 2
  addEventListener(
    type: string,
    listener: (ev: { data: string; lastEventId?: string }) => void,
  ): void
  onerror: ((ev: unknown) => void) | null
  onopen: ((ev: unknown) => void) | null
  close(): void
}

export const ApiClientKey: InjectionKey<ApiClient> = Symbol('ApiClient')
export const FetchKey: InjectionKey<typeof fetch> = Symbol('Fetch')
export const EventSourceFactoryKey: InjectionKey<(url: string) => EventSourceLike> =
  Symbol('EventSourceFactory')

export function createApiClient(opts: { baseUrl?: string; fetch?: typeof fetch } = {}): ApiClient {
  const baseUrl = opts.baseUrl ?? ''
  const doFetch: typeof fetch = opts.fetch ?? ((...args) => globalThis.fetch(...args))

  return {
    async request<T>(method: string, path: string, ro: RequestOptions = {}): Promise<T> {
      let url = baseUrl + path
      if (ro.query) {
        const qs = new URLSearchParams()
        for (const [k, v] of Object.entries(ro.query)) qs.set(k, String(v))
        const s = qs.toString()
        if (s) url += (url.includes('?') ? '&' : '?') + s
      }
      const headers: Record<string, string> = { Accept: 'application/json', ...ro.headers }
      const init: RequestInit = { method, headers }
      if (ro.body !== undefined) {
        headers['Content-Type'] = 'application/json'
        init.body = JSON.stringify(ro.body)
      }

      let res: Response
      try {
        res = await doFetch(url, init)
      } catch (e) {
        throw networkError(e)
      }

      const requestId = res.headers?.get?.('X-Request-ID') ?? null
      const text = await res.text().catch(() => '')
      let body: unknown = undefined
      if (text) {
        try {
          body = JSON.parse(text)
        } catch {
          body = undefined
        }
      }
      if (!res.ok) throw apiErrorFromBody(res.status, body, requestId)
      return body as T
    },
  }
}
