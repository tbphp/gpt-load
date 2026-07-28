import {
  ApiError,
  InvalidRequestPathError,
  InvalidResponseError,
  NetworkError,
  RequestCancelledError,
} from './errors'
import type { AppLocale, ErrorEnvelope, SuccessEnvelope } from './types'

export type ApiPath = `/api/${string}`

export interface ApiClientDependencies {
  fetch: typeof fetch
  getAuthKey(): string
  getLocale(): AppLocale
  onUnauthorized(): void
}

export interface ApiRequestOptions extends Omit<RequestInit, 'body' | 'headers'> {
  authKey?: string
  headers?: HeadersInit
  json?: unknown
  handleUnauthorized?: boolean
}

export interface ApiResponse<T> {
  data: T
  status: number
  headers: Headers
}

export interface ApiClient {
  request<T = unknown>(path: ApiPath, options?: ApiRequestOptions): Promise<T>
  requestWithResponse?<T = unknown>(
    path: ApiPath,
    options?: ApiRequestOptions,
  ): Promise<ApiResponse<T>>
}

export interface ApiClientWithResponse extends ApiClient {
  requestWithResponse<T = unknown>(
    path: ApiPath,
    options?: ApiRequestOptions,
  ): Promise<ApiResponse<T>>
}

type Envelope = SuccessEnvelope<unknown> | ErrorEnvelope

const apiPathBase = 'https://gpt-load.invalid'

function isSafeApiPath(path: unknown): path is ApiPath {
  if (typeof path !== 'string' || !path.startsWith('/api/')) {
    return false
  }

  try {
    decodeURIComponent(path)
  } catch {
    return false
  }

  const rawPathname = path.split(/[?#]/, 1)[0]
  if (!rawPathname) {
    return false
  }

  for (const segment of rawPathname.split('/')) {
    let decodedSegment: string
    try {
      decodedSegment = decodeURIComponent(segment)
    } catch {
      return false
    }

    if (
      decodedSegment === '.' ||
      decodedSegment === '..' ||
      decodedSegment.includes('/') ||
      decodedSegment.includes('\\')
    ) {
      return false
    }
  }

  const target = new URL(path, apiPathBase)
  return target.origin === apiPathBase && target.pathname.startsWith('/api/')
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

async function parseEnvelope(response: Response): Promise<Envelope> {
  let parsed: unknown
  try {
    parsed = JSON.parse(await response.text())
  } catch {
    throw new InvalidResponseError()
  }

  if (!isRecord(parsed) || typeof parsed.message !== 'string') {
    throw new InvalidResponseError()
  }

  if (parsed.code === 0) {
    return {
      code: 0,
      message: parsed.message,
      data: parsed.data,
    }
  }

  if (typeof parsed.code === 'string' && parsed.code.length > 0) {
    return {
      code: parsed.code,
      message: parsed.message,
      data: parsed.data,
    }
  }

  throw new InvalidResponseError()
}

function readRetryAfter(data: unknown, headers: Headers): number | undefined {
  if (
    isRecord(data) &&
    typeof data.retry_after_seconds === 'number' &&
    Number.isFinite(data.retry_after_seconds) &&
    data.retry_after_seconds > 0
  ) {
    return Math.ceil(data.retry_after_seconds)
  }

  const retryAfter = headers.get('Retry-After')
  if (retryAfter !== null && /^[1-9]\d*$/.test(retryAfter)) {
    return Number.parseInt(retryAfter, 10)
  }

  return undefined
}

export function createApiClient(deps: ApiClientDependencies): ApiClientWithResponse {
  let unauthorizedHandled = false
  let generation = 0

  const handleUnauthorizedStatus = (
    status: number,
    enabled: boolean,
    requestGeneration: number,
  ) => {
    if (status === 401 && enabled && !unauthorizedHandled && requestGeneration === generation) {
      unauthorizedHandled = true
      generation += 1
      deps.onUnauthorized()
    }
  }

  const handleSuccessfulResponse = (requestGeneration: number) => {
    if (unauthorizedHandled && requestGeneration === generation) {
      unauthorizedHandled = false
      generation += 1
    }
  }

  async function requestWithResponse<T>(
    path: ApiPath,
    options: ApiRequestOptions = {},
  ): Promise<ApiResponse<T>> {
    if (!isSafeApiPath(path)) {
      throw new InvalidRequestPathError()
    }

    const requestGeneration = generation
    const {
      authKey = deps.getAuthKey(),
      headers: callerHeaders,
      json,
      handleUnauthorized = true,
      ...requestInit
    } = options
    const headers = new Headers(callerHeaders)
    if (authKey) {
      headers.set('Authorization', `Bearer ${authKey}`)
    } else {
      headers.delete('Authorization')
    }
    headers.set('Accept-Language', deps.getLocale())
    if (json !== undefined) {
      headers.set('Content-Type', 'application/json')
    }

    let result: Response
    try {
      result = await deps.fetch(path, {
        ...requestInit,
        headers,
        body: json === undefined ? undefined : JSON.stringify(json),
      })
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
        throw new RequestCancelledError()
      }
      throw new NetworkError()
    }

    let envelope: Envelope
    try {
      envelope = await parseEnvelope(result)
    } catch (error) {
      handleUnauthorizedStatus(result.status, handleUnauthorized, requestGeneration)
      throw error
    }

    handleUnauthorizedStatus(result.status, handleUnauthorized, requestGeneration)
    if (result.ok && envelope.code === 0) {
      handleSuccessfulResponse(requestGeneration)
      return {
        data: envelope.data as T,
        status: result.status,
        headers: new Headers(result.headers),
      }
    }
    if (envelope.code === 0) {
      throw new InvalidResponseError()
    }

    const retryAfterSeconds =
      result.status === 429 ? readRetryAfter(envelope.data, result.headers) : undefined
    throw new ApiError(
      result.status,
      envelope.code,
      envelope.message,
      envelope.data,
      retryAfterSeconds,
    )
  }

  return {
    async request<T>(path: ApiPath, options?: ApiRequestOptions) {
      return (await requestWithResponse<T>(path, options)).data
    },
    requestWithResponse,
  }
}
