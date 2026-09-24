import { expect, test } from '@playwright/test'
import { readFileSync } from 'node:fs'

type Route = { name: string; path: string }
const common = JSON.parse(
  readFileSync(new URL('../../internal/webui/page_routes.json', import.meta.url), 'utf8'),
) as { routes: Route[] }
const modernOnly = JSON.parse(
  readFileSync(new URL('../../internal/webui/modern_page_routes.json', import.meta.url), 'utf8'),
) as { routes: Route[] }
const routes = [...common.routes, ...modernOnly.routes]

const authKey = process.env.RELEASE_TEST_AUTH_KEY
const groupID = process.env.RELEASE_TEST_GROUP_ID
const groupName = process.env.RELEASE_TEST_GROUP_NAME
const accessKeyName = process.env.RELEASE_TEST_ACCESS_KEY_NAME
const accessKeyValue = process.env.RELEASE_TEST_ACCESS_KEY_VALUE

if (
  !process.env.RELEASE_TEST_BASE_URL ||
  !authKey ||
  !groupID ||
  !groupName ||
  !accessKeyName ||
  !accessKeyValue
) {
  throw new Error('Release browser test environment is incomplete')
}

function path(name: string): string {
  const item = routes.find((route) => route.name === name)
  if (!item) throw new Error(`Unknown release page ${name}`)
  return item.path.replace(':id', groupID!)
}

async function installSession(
  page: import('@playwright/test').Page,
  frontend: 'modern' | 'classic',
  credential = authKey!,
) {
  await page.addInitScript(
    ({ auth, selected }) => {
      if (sessionStorage.getItem('gpt-load.release-e2e-initialized') === '1') return
      localStorage.setItem('gpt-load.auth-key', auth)
      if (selected === 'classic') localStorage.setItem('gpt-load.frontend.v2', 'classic')
      else localStorage.removeItem('gpt-load.frontend.v2')
      sessionStorage.setItem('gpt-load.release-e2e-initialized', '1')
    },
    { auth: credential, selected: frontend },
  )
}

async function assertPage(
  page: import('@playwright/test').Page,
  frontend: 'modern' | 'classic',
  target: string,
  expectedAPI?: string,
) {
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  const response = expectedAPI
    ? page.waitForResponse((item) => {
        const url = new URL(item.url())
        return url.pathname.startsWith(expectedAPI) && item.request().method() === 'GET'
      })
    : undefined
  await page.goto(target)
  if (response) expect((await response).ok(), `${expectedAPI} failed`).toBe(true)
  await expect(page.locator('html')).toHaveAttribute('data-frontend', frontend)
  await expect(page).not.toHaveURL(/\/login(?:[?#]|$)/u)
  await expect(page.getByRole('heading', { level: 1 }).first()).toBeVisible()
  await expect(page.getByRole('main')).toBeVisible()
  expect(errors, `JavaScript errors at ${target}`).toEqual([])
}

test.describe('新版主要页面', () => {
  test.beforeEach(async ({ page }) => installSession(page, 'modern'))

  for (const name of [
    'home',
    'groups',
    'group-detail',
    'models',
    'access-keys',
    'monitor-logs',
    'monitor-usage',
    'monitor-health',
    'settings',
  ]) {
    test(name, async ({ page }, testInfo) => {
      const api: Record<string, string> = {
        home: '/api/home',
        groups: '/api/modern/groups',
        'group-detail': `/api/groups/${groupID}`,
        models: '/api/models',
        'access-keys': '/api/modern/access-keys',
        'monitor-logs': '/api/logs',
        'monitor-usage': '/api/usage',
        'monitor-health': '/api/health',
        settings: '/api/settings',
      }
      await assertPage(page, 'modern', path(name), api[name])
      if (name === 'groups' || name === 'group-detail') {
        await expect(page.getByText(groupName!, { exact: false }).first()).toBeVisible()
      }
      if (name === 'access-keys') {
        await expect(page.getByText(accessKeyName!, { exact: false }).first()).toBeVisible()
      }
      if (name === 'settings') {
        await expect(page.locator('#settings-section-interface')).toBeVisible()
      }
      if (['home', 'groups', 'settings'].includes(name)) {
        await testInfo.attach(`modern-${name}`, {
          body: await page.screenshot({ fullPage: true }),
          contentType: 'image/png',
        })
      }
    })
  }
})

test.describe('经典版主要页面', () => {
  test.beforeEach(async ({ page }) => installSession(page, 'classic'))

  for (const name of [
    'home',
    'groups',
    'group-detail',
    'models',
    'access-keys',
    'import',
    'settings',
  ]) {
    test(name, async ({ page }, testInfo) => {
      const api: Record<string, string> = {
        home: '/api/home',
        groups: '/api/groups',
        'group-detail': `/api/groups/${groupID}`,
        models: '/api/models',
        'access-keys': '/api/access-keys',
        settings: '/api/settings',
      }
      await assertPage(page, 'classic', path(name), api[name])
      if (name === 'groups' || name === 'group-detail') {
        await expect(page.getByText(groupName!, { exact: false }).first()).toBeVisible()
      }
      if (name === 'access-keys') {
        await expect(page.getByText(accessKeyName!, { exact: false }).first()).toBeVisible()
      }
      if (name === 'settings') {
        await expect(page.locator('#settings-interface')).toBeVisible()
      }
      if (['home', 'groups', 'settings'].includes(name)) {
        await testInfo.attach(`classic-${name}`, {
          body: await page.screenshot({ fullPage: true }),
          contentType: 'image/png',
        })
      }
    })
  }

  for (const tab of ['usage', 'logs', 'health', 'inspector']) {
    test(`monitor ${tab}`, async ({ page }) => {
      const api: Record<string, string> = {
        usage: '/api/usage',
        logs: '/api/logs',
        health: '/api/health',
      }
      await assertPage(page, 'classic', `${path('monitor')}?tab=${tab}`, api[tab])
      await expect(page.getByRole('tab', { selected: true })).toBeVisible()
    })
  }
})

test('设置页切换界面后保存浏览器偏好', async ({ page }) => {
  await installSession(page, 'modern')
  await assertPage(page, 'modern', `${path('settings')}?section=interface`, '/api/settings')
  await page.getByRole('button', { name: /经典版/u }).click()
  await expect(page.locator('html')).toHaveAttribute('data-frontend', 'classic')
  await expect(page.locator('#settings-interface')).toBeVisible()
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('gpt-load.frontend.v2')))
    .toBe('classic')
  await page.getByRole('button', { name: /现代版/u }).click()
  await expect(page.locator('html')).toHaveAttribute('data-frontend', 'modern')
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('gpt-load.frontend.v2')))
    .toBeNull()
})

test('访问密钥身份只进入新版授权页面', async ({ page }) => {
  await installSession(page, 'classic', accessKeyValue)
  await assertPage(page, 'modern', path('models'), '/api/models')
  await page.goto(path('settings'))
  await expect(page.locator('html')).toHaveAttribute('data-frontend', 'modern')
  await expect(page).toHaveURL(/\/$/u)
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('gpt-load.frontend.v2')))
    .toBeNull()
})
