import { QueryClient } from '@tanstack/vue-query'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive, readonly } from 'vue'
import { createMemoryHistory } from 'vue-router'

import { ApiError, InvalidResponseError, NetworkError } from '@/api/errors'
import { createAppRouter } from '@/app/router'
import {
  authSessionKey,
  createAuthSession,
  type AuthSession,
  type AuthState,
} from '@/features/auth/auth-session'
import { createAppI18n, type AppLocale } from '@/i18n'

import LoginView from './LoginView.vue'

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function createMemoryStorage(): Storage {
  const values = new Map<string, string>()
  return {
    get length() {
      return values.size
    },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => values.delete(key),
    setItem: (key, value) => values.set(key, value),
  }
}

function createFakeSession(
  loginImpl: (candidate: string) => Promise<void> = async () => {},
  initialState: AuthState = { phase: 'anonymous', retryAfterSeconds: 0 },
): AuthSession {
  const state = reactive<AuthState>(initialState)
  let credential = ''

  return {
    state: readonly(state),
    getAuthKey: () => credential,
    hasCredential: () => credential.length > 0,
    async ensureValidated() {},
    async login(candidate) {
      await loginImpl(candidate)
      credential = candidate
      state.phase = 'validated'
      state.retryAfterSeconds = 0
    },
    async retryValidation() {},
    clear() {
      credential = ''
      state.phase = 'anonymous'
      state.retryAfterSeconds = 0
    },
  }
}

async function mountLogin(
  session: AuthSession,
  options: {
    locale?: AppLocale
    redirect?: string
  } = {},
) {
  const router = createAppRouter(session, createMemoryHistory())
  await router.push({
    name: 'login',
    query: options.redirect === undefined ? {} : { redirect: options.redirect },
  })
  await router.isReady()
  const appI18n = createAppI18n(undefined, options.locale ?? 'zh-CN')
  const wrapper = mount(LoginView, {
    global: {
      plugins: [appI18n.plugin, router],
      provide: {
        [authSessionKey as symbol]: session,
      },
    },
  })
  return { router, wrapper }
}

async function submitCredential(wrapper: ReturnType<typeof mount>, candidate: string) {
  await wrapper.get('input[type="password"]').setValue(candidate)
  await wrapper.get('form').trigger('submit')
}

describe('LoginView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it.each(['', ' ', ' leading', 'trailing ', 'middle space', 'non\u00a0breaking', '\t'])(
    'does not submit an empty or whitespace-containing AUTH_KEY',
    async (candidate) => {
      const login = vi.fn(async () => {})
      const { wrapper } = await mountLogin(createFakeSession(login))

      await submitCredential(wrapper, candidate)

      expect(login).not.toHaveBeenCalled()
      expect(wrapper.get('[role="alert"]').text()).not.toBe('')
    },
  )

  it('submits once on form submit and disables duplicate submission', async () => {
    const validation = deferred<void>()
    const login = vi.fn(() => validation.promise)
    const { wrapper } = await mountLogin(createFakeSession(login))

    await wrapper.get('input').setValue('candidate-key')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')

    expect(login).toHaveBeenCalledTimes(1)
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('button[type="submit"]').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('input').attributes()).toHaveProperty('disabled')

    validation.resolve()
    await flushPromises()
  })

  it('uses a semantic form and submit button', async () => {
    const login = vi.fn(async () => {})
    const { wrapper } = await mountLogin(createFakeSession(login))

    await wrapper.get('input').setValue('candidate-key')
    expect(wrapper.get('button').attributes('type')).toBe('submit')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(login).toHaveBeenCalledWith('candidate-key')
  })

  it('combines the brand and localized sign-in title into one semantic heading', async () => {
    const { wrapper } = await mountLogin(createFakeSession())

    const title = wrapper.get('h1#login-title')
    expect(wrapper.findAll('h1')).toHaveLength(1)
    expect(title.text()).toContain('GPT-Load')
    expect(title.text()).toContain('登录管理界面')
    expect(title.get('.login-brand__mark').attributes('aria-hidden')).toBe('true')
    expect(wrapper.get('#login-description').classes()).toContain('sr-only')
    expect(wrapper.get('.login-card').attributes('aria-describedby')).toBe('login-description')
  })

  it('keeps AUTH_KEY rescue information in a compact note without a separate title or list', async () => {
    const { wrapper } = await mountLogin(createFakeSession())

    const help = wrapper.get('.login-help')
    expect(help.find('h2').exists()).toBe(false)
    expect(help.find('ul').exists()).toBe(false)
    expect(help.text()).toContain('AUTH_KEY')
    expect(help.text()).toContain('${DATA_DIR}/auth.key')
    expect(help.text()).toContain('docker compose exec')
  })

  it('stores the credential only after session.login succeeds', async () => {
    const validation = deferred<{ authenticated: boolean }>()
    const storage = createMemoryStorage()
    const session = createAuthSession({
      storage,
      queryClient: new QueryClient(),
      validate: () => validation.promise,
    })
    const { wrapper } = await mountLogin(session)

    await submitCredential(wrapper, 'candidate-key')

    expect(session.getAuthKey()).toBe('')
    expect(storage.getItem('gpt-load.auth-key')).toBeNull()

    validation.resolve({ authenticated: true })
    await flushPromises()

    expect(session.getAuthKey()).toBe('candidate-key')
    expect(storage.getItem('gpt-load.auth-key')).toBe('candidate-key')
  })

  it('redirects to a registered safe target after success', async () => {
    const { router, wrapper } = await mountLogin(createFakeSession(), {
      redirect: '/monitor?tab=requests',
    })

    await submitCredential(wrapper, 'candidate-key')
    await flushPromises()

    expect(router.currentRoute.value.fullPath).toBe('/monitor?tab=requests')
  })

  it.each(['https://evil.example/', '//evil.example/', '/not-registered'])(
    'falls back to home for external, double-slash and unknown redirects',
    async (redirect) => {
      const { router, wrapper } = await mountLogin(createFakeSession(), { redirect })

      await submitCredential(wrapper, 'candidate-key')
      await flushPromises()

      expect(router.currentRoute.value.fullPath).toBe('/')
    },
  )

  it('renders invalid credential feedback for UNAUTHORIZED', async () => {
    const session = createFakeSession(async () => {
      throw new ApiError(401, 'UNAUTHORIZED', 'UNAUTHORIZED')
    })
    const { wrapper } = await mountLogin(session)

    await submitCredential(wrapper, 'wrong-key')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('AUTH_KEY 无效')
    expect(wrapper.text()).not.toContain('无法连接到管理 API')
  })

  it('renders a service-driven countdown for AUTH_LOCKED', async () => {
    const session = createFakeSession(async () => {
      throw new ApiError(429, 'AUTH_LOCKED', 'AUTH_LOCKED', undefined, 2)
    })
    const { wrapper } = await mountLogin(session)

    await submitCredential(wrapper, 'candidate-key')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('请在 2 秒后重试')
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty('disabled')

    vi.advanceTimersByTime(1_000)
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[role="alert"]').text()).toContain('请在 1 秒后重试')
  })

  it('uses the current AUTH_LOCKED response instead of a stale session countdown', async () => {
    const session = createFakeSession(
      async () => {
        throw new ApiError(429, 'AUTH_LOCKED', 'AUTH_LOCKED', undefined, 2)
      },
      { phase: 'locked', retryAfterSeconds: 99 },
    )
    const { wrapper } = await mountLogin(session)

    await submitCredential(wrapper, 'candidate-key')
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('请在 2 秒后重试')
    expect(wrapper.get('[role="alert"]').text()).not.toContain('99 秒')
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty('disabled')
  })

  it('re-enables submit when the countdown reaches zero', async () => {
    const session = createFakeSession(async () => {
      throw new ApiError(429, 'AUTH_LOCKED', 'AUTH_LOCKED', undefined, 1)
    })
    const { wrapper } = await mountLogin(session)

    await submitCredential(wrapper, 'candidate-key')
    await flushPromises()
    expect(wrapper.get('button[type="submit"]').attributes()).toHaveProperty('disabled')

    vi.advanceTimersByTime(1_000)
    await wrapper.vm.$nextTick()

    expect(wrapper.get('button[type="submit"]').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('renders network and invalid-response feedback separately', async () => {
    const network = await mountLogin(
      createFakeSession(async () => {
        throw new NetworkError()
      }),
    )
    await submitCredential(network.wrapper, 'candidate-key')
    await flushPromises()
    expect(network.wrapper.get('[role="alert"]').text()).toContain('无法连接到管理 API')
    expect(network.wrapper.text()).not.toContain('无法识别的响应')

    const invalid = await mountLogin(
      createFakeSession(async () => {
        throw new InvalidResponseError()
      }),
    )
    await submitCredential(invalid.wrapper, 'candidate-key')
    await flushPromises()
    expect(invalid.wrapper.get('[role="alert"]').text()).toContain('无法识别的响应')
    expect(invalid.wrapper.text()).not.toContain('无法连接到管理 API')
  })

  it('never renders the entered AUTH_KEY outside the password input', async () => {
    const authKeyCanary = 'AUTH_KEY_CANARY_9f31'
    const session = createFakeSession(async () => {
      throw new ApiError(401, 'UNAUTHORIZED', 'UNAUTHORIZED')
    })
    const { router, wrapper } = await mountLogin(session)

    await submitCredential(wrapper, authKeyCanary)
    await flushPromises()

    expect((wrapper.get('input').element as HTMLInputElement).value).toBe(authKeyCanary)
    expect(wrapper.text()).not.toContain(authKeyCanary)
    expect(JSON.stringify(router.currentRoute.value.query)).not.toContain(authKeyCanary)
    expect(JSON.stringify(session.state)).not.toContain(authKeyCanary)
  })

  it.each([
    [
      'zh-CN',
      [
        '登录管理界面',
        '输入管理 AUTH_KEY 以访问控制面。',
        '日志只显示文件路径',
        'docker compose exec',
      ],
    ],
    [
      'en-US',
      [
        'Sign in to the admin interface',
        'Enter the management AUTH_KEY to access the control plane.',
        'Logs show only the file path',
        'docker compose exec',
      ],
    ],
    [
      'ja-JP',
      [
        '管理画面にログイン',
        'コントロールプレーンにアクセスするための管理用 AUTH_KEY',
        'ファイルパスだけ',
        'docker compose exec',
      ],
    ],
  ] as const)('renders complete localized login content for %s', async (locale, expected) => {
    const { wrapper } = await mountLogin(createFakeSession(), { locale })

    for (const fragment of expected) {
      expect(wrapper.text()).toContain(fragment)
    }
    expect(wrapper.get('label').text()).toBe('AUTH_KEY')
    expect(wrapper.get('button[type="submit"]').text()).not.toMatch(/^auth\./)
  })
})
