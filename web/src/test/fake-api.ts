import type { ApiClient, ApiPath, ApiRequestOptions } from '@/api/client'

interface DeferredRoute {
  promise: Promise<unknown>
  resolve(value: unknown): void
  reject(error: unknown): void
}

export interface FakeApiRequest {
  path: ApiPath
  options?: ApiRequestOptions
}

function deferredRoute(): DeferredRoute {
  let resolve!: (value: unknown) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<unknown>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

export class FakeApi implements ApiClient {
  readonly requests: FakeApiRequest[] = []
  private readonly routes = new Map<ApiPath, DeferredRoute>()

  when(path: ApiPath): DeferredRoute {
    const existing = this.routes.get(path)
    if (existing) return existing
    const route = deferredRoute()
    this.routes.set(path, route)
    return route
  }

  async request<T>(path: ApiPath, options?: ApiRequestOptions): Promise<T> {
    this.requests.push({ path, options })
    const route = this.routes.get(path)
    if (!route) throw new Error('FAKE_API_ROUTE_NOT_CONFIGURED')
    return route.promise as Promise<T>
  }
}
