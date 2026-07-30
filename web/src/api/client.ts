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

const maxApiPathLength = 8192
const maxPercentEncodingDepth = 2

function hasControlCharacter(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0)
    if (codePoint !== undefined && (codePoint <= 0x1f || codePoint === 0x7f)) {
      return true
    }
  }

  return false
}

function isSafeApiQuery(query: string): boolean {
  try {
    decodeURIComponent(query)
  } catch {
    return false
  }

  let encodedQuery = query
  for (let encodingDepth = 1; encodingDepth <= maxPercentEncodingDepth; encodingDepth += 1) {
    if (/%(?:0[0-9a-f]|1[0-9a-f]|7f)/i.test(encodedQuery)) {
      return false
    }

    const expandedQuery = encodedQuery.replace(/%25/gi, '%')
    if (expandedQuery === encodedQuery) {
      return true
    }
    if (encodingDepth === maxPercentEncodingDepth) {
      return false
    }
    encodedQuery = expandedQuery
  }

  return false
}

function isSafeApiPathSegment(segment: string): boolean {
  let decodedSegment = segment
  for (let encodingDepth = 0; encodingDepth <= maxPercentEncodingDepth; encodingDepth += 1) {
    if (
      decodedSegment.length === 0 ||
      decodedSegment === '.' ||
      decodedSegment === '..' ||
      decodedSegment.includes('/') ||
      decodedSegment.includes('\\') ||
      decodedSegment.includes('?') ||
      decodedSegment.includes('#') ||
      hasControlCharacter(decodedSegment)
    ) {
      return false
    }

    if (!decodedSegment.includes('%')) {
      return true
    }
    if (encodingDepth === maxPercentEncodingDepth) {
      return false
    }

    try {
      decodedSegment = decodeURIComponent(decodedSegment)
    } catch {
      return false
    }
  }

  return false
}

function isSafeApiPath(path: unknown): path is ApiPath {
  if (
    typeof path !== 'string' ||
    path.length > maxApiPathLength ||
    !path.startsWith('/api/') ||
    path.startsWith('//') ||
    path.includes('\\') ||
    path.includes('#') ||
    hasControlCharacter(path)
  ) {
    return false
  }

  const queryStart = path.indexOf('?')
  const pathname = queryStart === -1 ? path : path.slice(0, queryStart)
  const query = queryStart === -1 ? '' : path.slice(queryStart + 1)
  if (!isSafeApiQuery(query)) {
    return false
  }

  const segments = pathname.slice('/api/'.length).split('/')
  return segments.every(isSafeApiPathSegment)
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
