import { QueryClient } from '@tanstack/vue-query'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, readonly } from 'vue'
import { createMemoryHistory } from 'vue-router'

import { ApiError, NetworkError } from '@/api/errors'
import type { AuthSessionPayload } from '@/api/types'
import {
  authSessionKey,
  createAuthSession,
  type AuthSession,
  type AuthState,
} from '@/features/auth/auth-session'
import { importRecoveryKey, type ImportRecoveryService } from '@/features/import/import-recovery'
import { createUnsavedChangesController, unsavedChangesKey } from '@/app/unsaved-changes'
import { createTestAppI18n as createAppI18n } from '@/test/i18n'

import AuthGate from './AuthGate.vue'
import { createAppRouter } from './router'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function createMemoryStorage(initial?: string): Storage {
  const values = new Map<string, string>()
  if (initial) values.set('gpt-load.auth-key', initial)
  return {
    get length() {
      return values.size
    },
    clear() {
      values.clear()
    },
    getItem(key) {
      return values.get(key) ?? null
    },
    key(index) {
      return [...values.keys()][index] ?? null
    },
    removeItem(key) {
      values.delete(key)
    },
    setItem(key, value) {
      values.set(key, value)
    },
  }
}

function createFakeSession(
  initialState: AuthState,
  overrides: Partial<AuthSession> = {},
): AuthSession {
  const state = reactive(initialState)
  let credential = 'restored-key'
  return {
    state: readonly(state),
    getAuthKey: () => credential,
    hasCredential: () => credential.length > 0,
    async ensureValidated() {},
    async login() {},
    async retryValidation() {},
    clear() {
      credential = ''
      state.phase = 'anonymous'
      state.retryAfterSeconds = 0
    },
    ...overrides,
  }
}

async function mountGate(session: AuthSession, path = '/login', attachTo?: Element) {
  const recovery: ImportRecoveryService = {
    register: () => () => {},
    captureForUnauthorized: () => 'no-active-draft',
    consume: () => null,
    clear: vi.fn(),
    sweep: () => {},
    dispose: () => {},
  }
  const router = createAppRouter(session, createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const appI18n = createAppI18n(undefined, 'zh-CN')
  const wrapper = mount(AuthGate, {
    ...(attachTo ? { attachTo } : {}),
    slots: {
      default: '<main>protected content</main>',
    },
    global: {
      plugins: [appI18n.plugin, router],
      provide: {
        [authSessionKey as symbol]: session,
        [importRecoveryKey as symbol]: recovery,
        [unsavedChangesKey as symbol]: createUnsavedChangesController(),
      },
    },
  })
  return { recovery, router, wrapper }
}

describe('AuthGate', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('validates once and renders protected content on success', async () => {
    const state = reactive<AuthState>({ phase: 'unvalidated', retryAfterSeconds: 0 })
    let validations = 0
    const session = createFakeSession(state, {
      ensureValidated: async () => {
        validations += 1
        state.phase = 'validating'
        await Promise.resolve()
        state.phase = 'validated'
      },
    })

    const { wrapper } = await mountGate(session)
    await flushPromises()

    expect(validations).toBe(1)
    expect(wrapper.text()).toContain('protected content')
  })

  it('shows one checking state during a shared validation', async () => {
    const validation = deferred<AuthSessionPayload>()
    let requests = 0
    const session = createAuthSession({
      storage: createMemoryStorage('restored-key'),
      queryClient: new QueryClient(),
      validate: () => {
        requests += 1
        return validation.promise
      },
    })

    const first = await mountGate(session)
    const second = await mountGate(session)

    expect(first.wrapper.get('[role="status"]').text()).toContain('正在验证当前会话…')
    expect(second.wrapper.get('[role="status"]').text()).toContain('正在验证当前会话…')
    expect(first.wrapper.get('section[aria-labelledby="auth-gate-title"]')).toBeDefined()
    expect(first.wrapper.get('[role="status"] [aria-hidden="true"]').text()).not.toBe('')
    expect(requests).toBe(1)

    validation.resolve({ authenticated: true })
    await flushPromises()
    expect(first.wrapper.text()).toContain('protected content')
    expect(second.wrapper.text()).toContain('protected content')
  })

  it('leaves 401 navigation to the global unauthorized handler', async () => {
    const state = reactive<AuthState>({ phase: 'unvalidated', retryAfterSeconds: 0 })
    const session = createFakeSession(state, {
      ensureValidated: async () => {
        state.phase = 'anonymous'
        throw new ApiError(401, 'UNAUTHORIZED', 'UNAUTHORIZED')
      },
    })

    const { router, wrapper } = await mountGate(session, '/')
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('home')
    expect(wrapper.text()).not.toContain('无权限')
    expect(wrapper.text()).not.toContain('AUTH_KEY 无效')
  })

  it('shows a localized lock countdown and retains the credential', async () => {
    const error = new ApiError(429, 'AUTH_LOCKED', 'locked', undefined, 2)
    const session = createFakeSession(
      { phase: 'locked', retryAfterSeconds: 2 },
      {
        ensureValidated: async () => {
          throw error
        },
      },
    )

    const { wrapper } = await mountGate(session)
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('认证尝试过多，请在 2 秒后重试。')
    expect(wrapper.get('[role="alert"] [aria-hidden="true"]').text()).not.toBe('')
    expect(session.getAuthKey()).toBe('restored-key')

    vi.advanceTimersByTime(1_000)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[role="alert"]').text()).toContain('认证尝试过多，请在 1 秒后重试。')
  })

  it('retries validation after the lock countdown', async () => {
    const state = reactive<AuthState>({ phase: 'locked', retryAfterSeconds: 1 })
    let retries = 0
    const session = createFakeSession(state, {
      ensureValidated: async () => {
        throw new ApiError(429, 'AUTH_LOCKED', 'locked', undefined, 1)
      },
      retryValidation: async () => {
        retries += 1
        state.phase = 'validated'
      },
    })

    const { wrapper } = await mountGate(session)
    await flushPromises()
    const retry = wrapper.get('button')
    expect(retry.attributes()).toHaveProperty('disabled')

    vi.advanceTimersByTime(1_000)
    await wrapper.vm.$nextTick()
    await retry.trigger('click')
    await flushPromises()

    expect(retries).toBe(1)
    expect(wrapper.text()).toContain('protected content')
  })

  it('restarts the lock countdown when retry returns the same server seconds', async () => {
    let validations = 0
    const session = createAuthSession({
      storage: createMemoryStorage('restored-key'),
      queryClient: new QueryClient(),
      validate: async () => {
        validations += 1
        throw new ApiError(429, 'AUTH_LOCKED', 'locked', undefined, 1)
      },
    })

    const { wrapper } = await mountGate(session)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('请在 1 秒后重试')
    expect(wrapper.get('button').attributes()).toHaveProperty('disabled')

    vi.advanceTimersByTime(1_000)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[role="alert"]').text()).toContain('请在 0 秒后重试')
    expect(wrapper.get('button').attributes()).not.toHaveProperty('disabled')

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(validations).toBe(2)
    expect(wrapper.get('[role="alert"]').text()).toContain('请在 1 秒后重试')
    expect(wrapper.get('button').attributes()).toHaveProperty('disabled')
    expect(session.getAuthKey()).toBe('restored-key')
  })

  it('clears the credential when choosing change AUTH_KEY', async () => {
    const state = reactive<AuthState>({ phase: 'locked', retryAfterSeconds: 2 })
    const session = createFakeSession(state, {
      ensureValidated: async () => {
        throw new ApiError(429, 'AUTH_LOCKED', 'locked', undefined, 2)
      },
    })
    const clear = vi.spyOn(session, 'clear')

    const { recovery, router, wrapper } = await mountGate(session, '/')
    await wrapper.findAll('button')[1]?.trigger('click')
    await flushPromises()

    expect(clear).toHaveBeenCalledOnce()
    expect(session.hasCredential()).toBe(false)
    expect(router.currentRoute.value.name).toBe('login')
    expect(recovery.clear).toHaveBeenCalledOnce()
  })

  it('shows a network retry state without clearing the credential', async () => {
    const state = reactive<AuthState>({ phase: 'network-error', retryAfterSeconds: 0 })
    let retries = 0
    const session = createFakeSession(state, {
      ensureValidated: async () => {
        throw new NetworkError()
      },
      retryValidation: async () => {
        retries += 1
        throw new NetworkError()
      },
    })

    const { wrapper } = await mountGate(session)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('无法连接到管理 API')
    expect(wrapper.get('[role="alert"] [aria-hidden="true"]').text()).not.toBe('')
    expect(wrapper.findAll('button').map((button) => button.text())).toEqual(['重试'])

    await wrapper.get('button').trigger('click')
    await flushPromises()

    expect(retries).toBe(1)
    expect(session.getAuthKey()).toBe('restored-key')
  })

  it('offers ordered recovery actions without exposing a rejected retry error', async () => {
    let retries = 0
    const session = createFakeSession(
      { phase: 'invalid-response', retryAfterSeconds: 0 },
      {
        ensureValidated: async () => {
          throw new Error('invalid response')
        },
        retryValidation: async () => {
          retries += 1
          throw new Error('external-invalid-response-canary')
        },
      },
    )

    const { wrapper } = await mountGate(session)
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('管理 API 返回了无法识别的响应。')
    expect(wrapper.text()).not.toContain('无法连接到管理 API')
    const actions = wrapper.findAll('button')
    expect(actions.map((button) => button.text())).toEqual(['重试', '更换 AUTH_KEY'])

    await actions[0]?.trigger('click')
    await flushPromises()

    expect(retries).toBe(1)
    expect(wrapper.get('[role="alert"]').text()).toContain('管理 API 返回了无法识别的响应。')
    expect(wrapper.text()).not.toContain('external-invalid-response-canary')
  })

  it('clears recovery and credential state when changing AUTH_KEY from invalid-response', async () => {
    const session = createFakeSession(
      { phase: 'invalid-response', retryAfterSeconds: 0 },
      {
        ensureValidated: async () => {
          throw new Error('invalid response')
        },
      },
    )
    const clear = vi.spyOn(session, 'clear')

    const { recovery, router, wrapper } = await mountGate(session, '/')
    await flushPromises()
    await wrapper.findAll('button')[1]?.trigger('click')
    await flushPromises()

    expect(recovery.clear).toHaveBeenCalledOnce()
    expect(clear).toHaveBeenCalledOnce()
    expect(session.hasCredential()).toBe(false)
    expect(router.currentRoute.value.name).toBe('login')
  })

  it('focuses Retry when mounted in invalid-response', async () => {
    const session = createFakeSession(
      { phase: 'invalid-response', retryAfterSeconds: 0 },
      {
        ensureValidated: async () => {
          throw new Error('invalid response')
        },
      },
    )

    const { wrapper } = await mountGate(session, '/login', document.body)
    try {
      await flushPromises()

      expect(document.activeElement).toBe(
        wrapper.get('[data-test="invalid-response-retry"]').element,
      )
    } finally {
      wrapper.unmount()
    }
  })

  it('focuses Retry when a mounted gate enters invalid-response', async () => {
    const state = reactive<AuthState>({ phase: 'network-error', retryAfterSeconds: 0 })
    const session = createFakeSession(state)

    const { wrapper } = await mountGate(session, '/login', document.body)
    try {
      await flushPromises()
      expect(document.activeElement).not.toBe(wrapper.get('button').element)

      state.phase = 'invalid-response'
      await flushPromises()

      expect(document.activeElement).toBe(
        wrapper.get('[data-test="invalid-response-retry"]').element,
      )
    } finally {
      wrapper.unmount()
    }
  })
})
