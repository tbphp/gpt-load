import {
  ApiError,
  InvalidRequestPathError,
  InvalidResponseError,
  NetworkError,
  RequestCancelledError,
} from './errors'
import { createApiClient, type ApiClientDependencies, type ApiPath } from './client'
import type { AppLocale, AuthSessionPayload } from './types'

function envelopeResponse(envelope: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(envelope), init)
}

function createDependencies(
  fetchImplementation: typeof fetch,
  overrides: Partial<ApiClientDependencies> = {},
): ApiClientDependencies {
  return {
    fetch: fetchImplementation,
    getAuthKey: () => 'current-key',
    getLocale: () => 'zh-CN',
    onUnauthorized: vi.fn(),
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve
  })

  return { promise, resolve }
}

describe('createApiClient', () => {
  it('parses a typed success envelope', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      envelopeResponse({
        code: 0,
        message: 'ok',
        data: { authenticated: true },
      }),
    )
    const client = createApiClient(createDependencies(fetchMock))

    const session = await client.request<AuthSessionPayload>('/api/auth/session')

    expect(session).toEqual({ authenticated: true })
  })

  it('sends the current bearer and locale on each request', async () => {
    let authKey = 'first-key'
    let locale: AppLocale = 'zh-CN'
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementation(async () => envelopeResponse({ code: 0, message: 'ok' }))
    const client = createApiClient(
      createDependencies(fetchMock, {
        getAuthKey: () => authKey,
        getLocale: () => locale,
      }),
    )

    await client.request('/api/groups')
    authKey = 'second-key'
    locale = 'ja-JP'
    await client.request('/api/groups')

    const firstHeaders = fetchMock.mock.calls[0]?.[1]?.headers as Headers
    const secondHeaders = fetchMock.mock.calls[1]?.[1]?.headers as Headers
    expect(firstHeaders.get('Authorization')).toBe('Bearer first-key')
    expect(firstHeaders.get('Accept-Language')).toBe('zh-CN')
    expect(secondHeaders.get('Authorization')).toBe('Bearer second-key')
    expect(secondHeaders.get('Accept-Language')).toBe('ja-JP')
  })

  it('uses a request bearer override without persisting it', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementation(async () => envelopeResponse({ code: 0, message: 'ok' }))
    const client = createApiClient(createDependencies(fetchMock))

    await client.request('/api/auth/session', {
      authKey: 'candidate-key',
    })
    await client.request('/api/auth/session')

    const overrideHeaders = fetchMock.mock.calls[0]?.[1]?.headers as Headers
    const currentHeaders = fetchMock.mock.calls[1]?.[1]?.headers as Headers
    expect(overrideHeaders.get('Authorization')).toBe('Bearer candidate-key')
    expect(currentHeaders.get('Authorization')).toBe('Bearer current-key')
  })

  it.each([
    'https://example.com/api/groups',
    '//example.com/api/groups',
    '/v1/models',
    '/api/../v1/models',
    '/api/%2e%2e/v1/models',
    '/api/%2E%2E/v1/models',
    '/api/%2e%2E/v1/models',
    '/api/%2e%2e%2fv1/models',
    '/api/%2e%2e%5cv1/models',
    '/api/%E0%A4%A',
    '/api/groups?filter=%E0%A4%A',
  ])('rejects unsafe path %s before reading or sending a bearer', async (unsafePath) => {
    const fetchMock = vi.fn<typeof fetch>()
    const getAuthKey = vi.fn(() => 'AUTH-KEY-MUST-NOT-LEAK')
    const client = createApiClient(createDependencies(fetchMock, { getAuthKey }))

    await expect(client.request(unsafePath as ApiPath)).rejects.toBeInstanceOf(
      InvalidRequestPathError,
    )
    expect(getAuthKey).not.toHaveBeenCalled()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('sets content-type only when json is present', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementation(async () => envelopeResponse({ code: 0, message: 'ok' }))
    const client = createApiClient(createDependencies(fetchMock))

    await client.request('/api/groups')
    await client.request('/api/groups', {
      method: 'POST',
      json: null,
    })

    const plainRequest = fetchMock.mock.calls[0]?.[1]
    const jsonRequest = fetchMock.mock.calls[1]?.[1]
    expect((plainRequest?.headers as Headers).has('Content-Type')).toBe(false)
    expect(plainRequest?.body).toBeUndefined()
    expect((jsonRequest?.headers as Headers).get('Content-Type')).toBe('application/json')
    expect(jsonRequest?.body).toBe('null')
  })

  it('throws ApiError with stable code and safe data', async () => {
    const secret = 'AUTH-KEY-MUST-NOT-LEAK'
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      envelopeResponse(
        {
          code: 'VALIDATION_ERROR',
          message: 'safe validation message',
          data: { field: 'safe-field' },
        },
        { status: 400 },
      ),
    )
    const client = createApiClient(createDependencies(fetchMock, { getAuthKey: () => secret }))

    const error = await client.request('/api/groups').catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({
      status: 400,
      code: 'VALIDATION_ERROR',
      message: 'safe validation message',
      data: { field: 'safe-field' },
    })
    expect((error as Error).message).not.toContain(secret)
    expect(JSON.stringify(error)).not.toContain(secret)
  })

  it('handles concurrent 401s once when an older success completes between them', async () => {
    const unauthorized = () =>
      envelopeResponse({ code: 'AUTH_UNAUTHORIZED', message: 'unauthorized' }, { status: 401 })
    const firstResponse = deferred<Response>()
    const secondResponse = deferred<Response>()
    const thirdResponse = deferred<Response>()
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementationOnce(() => firstResponse.promise)
      .mockImplementationOnce(() => secondResponse.promise)
      .mockImplementationOnce(() => thirdResponse.promise)
    const onUnauthorized = vi.fn()
    const client = createApiClient(createDependencies(fetchMock, { onUnauthorized }))

    const firstRequest = client.request('/api/groups')
    const secondRequest = client.request('/api/access-keys')
    const thirdRequest = client.request('/api/groups')

    firstResponse.resolve(unauthorized())
    await expect(firstRequest).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)

    secondResponse.resolve(envelopeResponse({ code: 0, message: 'ok' }))
    await expect(secondRequest).resolves.toBeUndefined()

    thirdResponse.resolve(unauthorized())
    await expect(thirdRequest).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('allows a new unauthorized incident after a genuinely subsequent success', async () => {
    const unauthorized = () =>
      envelopeResponse({ code: 'AUTH_UNAUTHORIZED', message: 'unauthorized' }, { status: 401 })
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementationOnce(async () => unauthorized())
      .mockImplementationOnce(async () => envelopeResponse({ code: 0, message: 'ok' }))
      .mockImplementationOnce(async () => unauthorized())
    const onUnauthorized = vi.fn()
    const client = createApiClient(createDependencies(fetchMock, { onUnauthorized }))

    await expect(client.request('/api/groups')).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)

    await client.request('/api/groups')
    await expect(client.request('/api/groups')).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).toHaveBeenCalledTimes(2)
  })

  it('handles a 401 once even when its envelope is invalid', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockImplementation(async () => new Response('not-json', { status: 401 }))
    const onUnauthorized = vi.fn()
    const client = createApiClient(createDependencies(fetchMock, { onUnauthorized }))

    const results = await Promise.allSettled([
      client.request('/api/groups'),
      client.request('/api/access-keys'),
    ])

    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(results).toEqual([
      expect.objectContaining({
        status: 'rejected',
        reason: expect.any(InvalidResponseError),
      }),
      expect.objectContaining({
        status: 'rejected',
        reason: expect.any(InvalidResponseError),
      }),
    ])
  })

  it('does not trigger global unauthorized handling when explicitly disabled', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        envelopeResponse({ code: 'AUTH_UNAUTHORIZED', message: 'unauthorized' }, { status: 401 }),
      )
    const onUnauthorized = vi.fn()
    const client = createApiClient(createDependencies(fetchMock, { onUnauthorized }))

    await expect(
      client.request('/api/auth/session', {
        handleUnauthorized: false,
      }),
    ).rejects.toBeInstanceOf(ApiError)
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('parses AUTH_LOCKED retry seconds without treating it as 401', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      envelopeResponse(
        {
          code: 'AUTH_LOCKED',
          message: 'locked',
          data: { retry_after_seconds: 1.2 },
        },
        {
          status: 429,
          headers: { 'Retry-After': '9' },
        },
      ),
    )
    const onUnauthorized = vi.fn()
    const client = createApiClient(createDependencies(fetchMock, { onUnauthorized }))

    const error = await client.request('/api/auth/session').catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({
      status: 429,
      code: 'AUTH_LOCKED',
      retryAfterSeconds: 2,
    })
    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  it('falls back to the Retry-After header when structured data is absent', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      envelopeResponse(
        { code: 'AUTH_LOCKED', message: 'locked' },
        {
          status: 429,
          headers: { 'Retry-After': '7' },
        },
      ),
    )
    const client = createApiClient(createDependencies(fetchMock))

    const error = await client.request('/api/auth/session').catch((reason: unknown) => reason)

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).retryAfterSeconds).toBe(7)
  })

  it('distinguishes network failure, cancellation and invalid response', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new Error('connection failed'))
      .mockRejectedValueOnce(new DOMException('cancelled', 'AbortError'))
      .mockResolvedValueOnce(new Response('not-json'))
    const client = createApiClient(createDependencies(fetchMock))

    await expect(client.request('/api/groups')).rejects.toBeInstanceOf(NetworkError)
    await expect(client.request('/api/groups')).rejects.toBeInstanceOf(RequestCancelledError)
    await expect(client.request('/api/groups')).rejects.toBeInstanceOf(InvalidResponseError)
  })

  it('never includes the bearer, URL or raw response in thrown messages', async () => {
    const secret = 'AUTH-KEY-MUST-NOT-LEAK'
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new Error(secret))
      .mockResolvedValueOnce(new Response(`invalid ${secret}`))
    const client = createApiClient(createDependencies(fetchMock, { getAuthKey: () => secret }))

    const networkError = await client.request('/api/groups').catch((reason: unknown) => reason)
    const invalidResponseError = await client
      .request('/api/groups')
      .catch((reason: unknown) => reason)
    const invalidPathError = await client
      .request(`https://example.com/${secret}` as ApiPath)
      .catch((reason: unknown) => reason)

    for (const error of [networkError, invalidResponseError, invalidPathError]) {
      expect(error).toBeInstanceOf(Error)
      expect((error as Error).message).not.toContain(secret)
      expect(JSON.stringify(error)).not.toContain(secret)
      expect((error as Error).message).not.toContain('example.com')
    }
  })
})
