import { QueryClient } from '@tanstack/vue-query'
import { mount } from '@vue/test-utils'
import { createMemoryHistory } from 'vue-router'

import App from './App.vue'
import { createAppRouter } from './app/router'
import { authSessionKey, createAuthSession } from './features/auth/auth-session'
import { createAppI18n } from './i18n'
import baseCss from './styles/base.css?raw'

function createMemoryStorage(credential?: string): Storage {
  const values = new Map<string, string>()
  if (credential) values.set('gpt-load.auth-key', credential)
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

async function mountAt(
  path: string,
  options: {
    credential?: string
    validate?: () => Promise<{ authenticated: boolean }>
  } = {},
) {
  const session = createAuthSession({
    storage: createMemoryStorage(options.credential),
    queryClient: new QueryClient(),
    validate: options.validate ?? (async () => ({ authenticated: true })),
  })
  const router = createAppRouter(session, createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const appI18n = createAppI18n(undefined, 'zh-CN')
  const wrapper = mount(App, {
    global: {
      plugins: [appI18n.plugin, router],
      provide: {
        [authSessionKey as symbol]: session,
      },
    },
  })
  return { router, wrapper }
}

describe('App', () => {
  it('keeps the approved login card padding above the mobile surface-card rule', () => {
    expect(baseCss).toMatch(/\.surface-card\.login-card\s*\{[^}]*padding:\s*26px 24px;/)
    expect(baseCss).toMatch(/\.surface-card\.login-card\s*\{[^}]*border-radius:\s*12px;/)
  })

  it('uses the compact reference rhythm for login fields and shared navigation', () => {
    expect(baseCss).toMatch(/body\s*\{[^}]*font-size:\s*14px;/)
    expect(baseCss).toMatch(/\.topbar\s*\{[^}]*gap:\s*20px;[^}]*padding:\s*8px 20px;/)
    expect(baseCss).toMatch(/\.nav\s*\{[^}]*gap:\s*20px;/)
    expect(baseCss).toMatch(/\.nav-link\s*\{[^}]*padding-inline:\s*2px;/)
    expect(baseCss).toMatch(/\.nav-link,\s*\.button-link\s*\{[^}]*min-height:\s*44px;/)
    expect(baseCss).toMatch(/\.nav-link\s*\{[^}]*border-bottom:\s*2px solid transparent;/)
    expect(baseCss).toMatch(
      /\.brand-mark\s*\{[^}]*width:\s*9px;[^}]*height:\s*9px;[^}]*border-radius:\s*3px;/,
    )
    expect(baseCss).toMatch(
      /\.nav-link\.router-link-active\s*\{[^}]*border-bottom-color:\s*var\(--color-primary\);/,
    )
    expect(baseCss).toMatch(/\.eyebrow\s*\{[^}]*color:\s*var\(--color-text-muted\);/)
    expect(baseCss).toMatch(
      /\.form-field__label\s*\{[^}]*color:\s*var\(--color-text-muted\);[^}]*font-size:\s*12px;/,
    )
    expect(baseCss).toMatch(
      /\.form-field input\s*\{[^}]*background:\s*var\(--color-surface-secondary\);[^}]*font-size:\s*13\.5px;/,
    )
    expect(baseCss).toMatch(
      /@media \(max-width:\s*640px\)\s*\{[\s\S]*?\.nav\s*\{[^}]*display:\s*none;/,
    )
    expect(baseCss).toMatch(
      /@media \(max-width:\s*640px\)\s*\{[\s\S]*?\.form-field input\s*\{[^}]*font-size:\s*16px;/,
    )
    expect(baseCss).toMatch(
      /\.sr-only\s*\{[^}]*position:\s*absolute;[^}]*width:\s*1px;[^}]*height:\s*1px;/,
    )
    expect(baseCss).toMatch(
      /@media \(max-width:\s*767px\)\s*\{[\s\S]*?\.topbar\s*\{[^}]*padding:\s*8px 20px;/,
    )
    expect(baseCss).toMatch(
      /@media \(max-width:\s*767px\)\s*\{[\s\S]*?\.page-content\s*\{[^}]*width:\s*min\(100% - 40px, 1280px\);/,
    )
    expect(baseCss).toMatch(
      /\.inline-feedback\s*\{[^}]*border-radius:\s*var\(--radius-control\);[^}]*padding:\s*9px 10px;[^}]*font-size:\s*0\.875rem;/,
    )
    expect(baseCss).toMatch(
      /\.inline-feedback--info\s*\{[^}]*background:\s*var\(--color-surface-secondary\);/,
    )
    expect(baseCss).toMatch(
      /\.inline-feedback--warning\s*\{[^}]*background:\s*var\(--color-warning-bg\);/,
    )
    expect(baseCss).toMatch(
      /\.inline-feedback--danger\s*\{[^}]*background:\s*var\(--color-danger-bg\);/,
    )
    expect(baseCss).toMatch(
      /\.login-form \.inline-feedback\s*\{[^}]*padding:\s*0;[^}]*font-size:\s*12px;/,
    )
    expect(baseCss).toMatch(
      /\.login-form \.inline-feedback--danger\s*\{[^}]*background:\s*transparent;/,
    )
    expect(baseCss).toMatch(
      /\.login-form \.inline-feedback--info\s*\{[^}]*background:\s*transparent;/,
    )
    expect(baseCss).toMatch(
      /\.login-form \.inline-feedback--warning\s*\{[^}]*background:\s*transparent;/,
    )
  })

  it('renders the S1 home placeholder', async () => {
    const { wrapper } = await mountAt('/', { credential: 'restored-key' })
    await vi.waitFor(() => {
      expect(wrapper.find('nav').exists()).toBe(true)
    })

    expect(wrapper.get('h1').text()).toBe('管理界面基础已就绪')
    expect(wrapper.get('nav').attributes('aria-label')).toBe('主导航')
  })

  it('does not validate the public login route', async () => {
    let validations = 0
    const { wrapper } = await mountAt('/login', {
      validate: async () => {
        validations += 1
        return { authenticated: true }
      },
    })

    expect(wrapper.get('h1').text()).toContain('GPT-Load登录管理界面')
    expect(wrapper.get('form').attributes()).toHaveProperty('novalidate')
    expect(validations).toBe(0)
  })

  it('renders a protected route only after validation', async () => {
    let finishValidation!: (payload: { authenticated: boolean }) => void
    const validation = new Promise<{ authenticated: boolean }>((resolve) => {
      finishValidation = resolve
    })
    const { wrapper } = await mountAt('/', {
      credential: 'restored-key',
      validate: () => validation,
    })

    expect(wrapper.find('nav').exists()).toBe(false)
    expect(wrapper.get('h1').text()).toBe('GPT-Load')
    expect(wrapper.get('[role="status"]').text()).toContain('正在验证当前会话…')

    finishValidation({ authenticated: true })
    await vi.waitFor(() => {
      expect(wrapper.find('nav').exists()).toBe(true)
    })
    expect(wrapper.get('h1').text()).toBe('管理界面基础已就绪')
  })
})
