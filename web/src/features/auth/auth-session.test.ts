import { QueryClient } from '@tanstack/vue-query'

import { ApiError, InvalidResponseError, NetworkError, RequestCancelledError } from '@/api/errors'
import type { AuthSessionPayload } from '@/api/types'

import {
  authSessionQueryKey,
  createAuthSession,
  type AuthSessionDependencies,
} from './auth-session'

const authStorageKey = 'gpt-load.auth-key'

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
}

function createDependencies(
  validate: AuthSessionDependencies['validate'] = async () => ({ authenticated: true }),
  storage: Storage | undefined = window.sessionStorage,
): AuthSessionDependencies {
  return {
    storage,
    queryClient: createQueryClient(),
    validate,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

beforeEach(() => {
  window.sessionStorage.clear()
  window.localStorage.clear()
})

describe('createAuthSession', () => {
  it('restores a credential from sessionStorage as unvalidated', () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')

    const session = createAuthSession(createDependencies())

    expect(session.getAuthKey()).toBe('restored-key')
    expect(session.hasCredential()).toBe(true)
    expect(session.state.phase).toBe('unvalidated')
  })

  it('does not read a credential from localStorage', () => {
    window.localStorage.setItem(authStorageKey, 'persistent-key')

    const session = createAuthSession(createDependencies())

    expect(session.getAuthKey()).toBe('')
    expect(session.hasCredential()).toBe(false)
    expect(session.state.phase).toBe('anonymous')
  })

  it('stores a candidate only after successful validation', async () => {
    const validation = deferred<AuthSessionPayload>()
    const session = createAuthSession(createDependencies(() => validation.promise))

    const login = session.login('candidate-key')

    expect(session.getAuthKey()).toBe('')
    expect(window.sessionStorage.getItem(authStorageKey)).toBeNull()

    validation.resolve({ authenticated: true })
    await login

    expect(session.getAuthKey()).toBe('candidate-key')
    expect(window.sessionStorage.getItem(authStorageKey)).toBe('candidate-key')
    expect(session.state.phase).toBe('validated')
  })

  it('keeps a failed candidate out of memory and storage', async () => {
    const session = createAuthSession(
      createDependencies(async () => {
        throw new ApiError(401, 'UNAUTHORIZED', 'UNAUTHORIZED')
      }),
    )

    await expect(session.login('wrong-key')).rejects.toMatchObject({
      code: 'UNAUTHORIZED',
    })

    expect(session.getAuthKey()).toBe('')
    expect(window.sessionStorage.getItem(authStorageKey)).toBeNull()
    expect(session.state.phase).toBe('anonymous')
  })

  it('clears memory, storage and query state on logout or 401', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    const dependencies = createDependencies(async () => {
      throw new ApiError(401, 'UNAUTHORIZED', 'UNAUTHORIZED')
    })
    const session = createAuthSession(dependencies)

    await expect(session.ensureValidated()).rejects.toMatchObject({
      code: 'UNAUTHORIZED',
    })

    expect(session.getAuthKey()).toBe('')
    expect(window.sessionStorage.getItem(authStorageKey)).toBeNull()
    expect(dependencies.queryClient.getQueryData(authSessionQueryKey)).toBeUndefined()
    expect(session.state.phase).toBe('anonymous')

    window.sessionStorage.setItem(authStorageKey, 'second-key')
    const logoutDependencies = createDependencies()
    const logoutSession = createAuthSession(logoutDependencies)
    logoutDependencies.queryClient.setQueryData(authSessionQueryKey, { authenticated: true })
    logoutSession.clear()

    expect(logoutSession.getAuthKey()).toBe('')
    expect(window.sessionStorage.getItem(authStorageKey)).toBeNull()
    expect(logoutDependencies.queryClient.getQueryData(authSessionQueryKey)).toBeUndefined()
  })

  it('retains a credential on lock and network failure', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    const errors = [new ApiError(429, 'AUTH_LOCKED', 'locked', undefined, 2.2), new NetworkError()]
    const session = createAuthSession(
      createDependencies(async () => {
        throw errors.shift()
      }),
    )

    await expect(session.ensureValidated()).rejects.toMatchObject({ code: 'AUTH_LOCKED' })
    expect(session.state.phase).toBe('locked')
    expect(session.state.retryAfterSeconds).toBe(3)
    expect(session.getAuthKey()).toBe('restored-key')

    await expect(session.retryValidation()).rejects.toBeInstanceOf(NetworkError)
    expect(session.state.phase).toBe('network-error')
    expect(session.getAuthKey()).toBe('restored-key')
    expect(window.sessionStorage.getItem(authStorageKey)).toBe('restored-key')
  })

  it('returns a cancelled validation to unvalidated without clearing', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    const session = createAuthSession(
      createDependencies(async () => {
        throw new RequestCancelledError()
      }),
    )

    await expect(session.ensureValidated()).rejects.toBeInstanceOf(RequestCancelledError)

    expect(session.state.phase).toBe('unvalidated')
    expect(session.getAuthKey()).toBe('restored-key')
    expect(window.sessionStorage.getItem(authStorageKey)).toBe('restored-key')
  })

  it('deduplicates concurrent validation to one promise and request', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    const validation = deferred<AuthSessionPayload>()
    let requests = 0
    const session = createAuthSession(
      createDependencies(() => {
        requests += 1
        return validation.promise
      }),
    )

    const first = session.ensureValidated()
    const second = session.ensureValidated()

    expect(first).toBe(second)
    expect(requests).toBe(1)

    validation.resolve({ authenticated: true })
    await first
  })

  it('ignores a stale validation result after the credential is cleared', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    const validation = deferred<AuthSessionPayload>()
    const session = createAuthSession(createDependencies(() => validation.promise))

    const pending = session.ensureValidated()
    session.clear()
    validation.resolve({ authenticated: true })
    await pending

    expect(session.state.phase).toBe('anonymous')
    expect(session.getAuthKey()).toBe('')
    expect(window.sessionStorage.getItem(authStorageKey)).toBeNull()
  })

  it('uses a fixed query key that never contains the credential', async () => {
    window.sessionStorage.setItem(authStorageKey, 'AUTH_KEY_CANARY')
    const dependencies = createDependencies()
    const session = createAuthSession(dependencies)

    await session.ensureValidated()

    const queryKeys = dependencies.queryClient
      .getQueryCache()
      .getAll()
      .map((query) => query.queryKey)
    expect(queryKeys).toContainEqual(['auth', 'session'])
    expect(JSON.stringify(queryKeys)).not.toContain('AUTH_KEY_CANARY')
  })

  it('does not retry an authentication request', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    let requests = 0
    const session = createAuthSession(
      createDependencies(async () => {
        requests += 1
        throw new NetworkError()
      }),
    )

    await expect(session.ensureValidated()).rejects.toBeInstanceOf(NetworkError)

    expect(requests).toBe(1)
  })

  it('allows validation retry after a transient state', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    let requests = 0
    const session = createAuthSession(
      createDependencies(async () => {
        requests += 1
        if (requests === 1) throw new NetworkError()
        return { authenticated: true }
      }),
    )

    await expect(session.ensureValidated()).rejects.toBeInstanceOf(NetworkError)
    await expect(session.retryValidation()).resolves.toBeUndefined()

    expect(requests).toBe(2)
    expect(session.state.phase).toBe('validated')
  })

  it('survives unavailable sessionStorage without persisting elsewhere', async () => {
    window.localStorage.setItem(authStorageKey, 'local-canary')
    const unavailableStorage = {
      getItem() {
        throw new DOMException('denied')
      },
      setItem() {
        throw new DOMException('denied')
      },
      removeItem() {
        throw new DOMException('denied')
      },
    } as unknown as Storage
    const session = createAuthSession(createDependencies(undefined, unavailableStorage))

    await session.login('memory-only-key')

    expect(session.getAuthKey()).toBe('memory-only-key')
    expect(session.state.phase).toBe('validated')
    expect(window.localStorage.getItem(authStorageKey)).toBe('local-canary')
  })

  it('maps an unrecognized validation payload separately', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    const session = createAuthSession(createDependencies(async () => ({ authenticated: false })))

    await expect(session.ensureValidated()).rejects.toBeInstanceOf(InvalidResponseError)

    expect(session.state.phase).toBe('invalid-response')
    expect(session.getAuthKey()).toBe('restored-key')
  })

  it('uses global unauthorized handling only for restored-session validation', async () => {
    window.sessionStorage.setItem(authStorageKey, 'restored-key')
    const requests: Array<{ key: string; globalUnauthorized: boolean }> = []
    const session = createAuthSession(
      createDependencies(async (key, globalUnauthorized) => {
        requests.push({ key, globalUnauthorized })
        return { authenticated: true }
      }),
    )

    await session.ensureValidated()
    session.clear()
    await session.login('candidate-key')

    expect(requests).toEqual([
      { key: 'restored-key', globalUnauthorized: true },
      { key: 'candidate-key', globalUnauthorized: false },
    ])
  })
})
