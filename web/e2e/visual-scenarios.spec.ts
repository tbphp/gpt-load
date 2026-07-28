import { expect, test } from './fixtures'
import { installVisualApi, visualAuthKey } from './visual-api'
import {
  captureVisualScenario,
  writeVisualScenarioManifest,
  type VisualCapture,
} from './visual-capture'
import {
  visualFixtureData,
  visualScenarioCases,
  visualScenarioLabel,
  type VisualScenarioCase,
} from './visual-fixtures'

const captures: VisualCapture[] = []

test.setTimeout(60_000)
test.afterAll(async () => {
  await writeVisualScenarioManifest(captures)
})

async function prepareScenario(
  page: Parameters<typeof installVisualApi>[0],
  item: VisualScenarioCase,
) {
  if (item.scenario === 'home-normal') {
    await expect(page.locator('[data-test="home-health-total"]')).toBeVisible()
    await expect(page.locator('[data-test="home-usage-requests"]')).toBeVisible()
    await expect(page.getByText(visualFixtureData.groupName, { exact: true })).toBeVisible()
    return
  }

  if (item.scenario === 'home-anomaly') {
    await expect(page.locator('[data-test="home-health-cooldown"]')).toHaveAttribute(
      'data-state',
      'anomaly',
    )
    const desktopSettings = page.locator('.desktop-nav a[href="/settings"]')
    if (await desktopSettings.isVisible()) {
      await desktopSettings.click()
    } else {
      await page.locator('.mobile-menu-trigger').click()
      await page.locator('.mobile-nav a[href="/settings"]').click()
    }
    await expect(page).toHaveURL('/settings')
    await page.locator('a.brand').click()
    await expect(page.locator('[data-test="home-operational-overview"]')).toBeVisible()
    await expect(page.getByText(/stale|旧数据/).first()).toBeVisible()
    return
  }

  if (item.scenario === 'home-empty-error') {
    await expect(page.locator('[data-test="home-usage-error"]')).toBeVisible()
    await expect(page.locator('[data-test="home-groups"]')).toBeVisible()
    return
  }

  if (item.scenario === 'access-keys-long') {
    await expect(page.getByText(visualFixtureData.longIdentifier, { exact: true })).toBeVisible()
    if (item.viewport.width === 375) {
      const statusLayout = await page
        .locator('.status-badge')
        .first()
        .evaluate((element) => {
          const style = getComputedStyle(element)
          return { flexShrink: style.flexShrink, whiteSpace: style.whiteSpace }
        })
      expect(statusLayout).toEqual({ flexShrink: '0', whiteSpace: 'nowrap' })
    }
    return
  }

  if (item.scenario === 'access-key-operation') {
    await page.locator('[data-test="access-key-create"]').click()
    const drawer = page.getByRole('dialog')
    await expect(drawer).toBeVisible()
    await drawer.locator('[data-test="access-key-name"]').fill('Visual pending AccessKey')
    await drawer.locator('[data-test="access-key-save"]').click()
    await expect(drawer.getByText(/unknown|未确认/).first()).toBeVisible()
    await drawer.locator('.app-drawer__close').click()
    await expect(page.locator('[data-test="access-key-operation-notice"]')).toBeVisible()
    return
  }

  if (item.scenario === 'model-prices-mixed') {
    await expect(page.locator('[data-test="builtin-price-row-0"]')).toBeVisible()
    await expect(page.locator('[data-test="override-price-row-0"]')).toBeVisible()
    return
  }

  if (item.scenario === 'settings-dirty' || item.scenario === 'settings-validation') {
    await page.locator('[data-test="override-request_timeout"]').check()
    await page
      .locator('[data-test="value-request_timeout"]')
      .fill(item.scenario === 'settings-dirty' ? '901' : '0')
    await expect(
      page.locator(
        item.scenario === 'settings-dirty'
          ? '[data-test="settings-dirty-summary"]'
          : '[data-test="settings-validation-summary"]',
      ),
    ).toBeVisible()
    return
  }

  if (item.scenario === 'usage-quality') {
    await expect(page.locator('[data-test="usage-window-quality"]')).toBeVisible()
    await expect(page.locator('[data-test="usage-process-health"]')).toBeVisible()
    return
  }

  if (item.scenario === 'logs-signal-path') {
    await expect(page.locator(`[data-test="log-row-${visualFixtureData.requestId}"]`)).toBeVisible()
    return
  }

  if (item.scenario === 'inspector-routing') {
    await page.locator('[data-test="inspector-submit"]').click()
    await expect(page.locator('[data-test="inspector-result"]')).toBeVisible()
  }
}

for (const item of visualScenarioCases) {
  test(`${visualScenarioLabel(item.scenario)} · ${item.id}`, async ({ page }, testInfo) => {
    await page.setViewportSize(item.viewport)
    await page.emulateMedia({
      colorScheme: item.theme,
      reducedMotion: 'reduce',
    })
    await page.addInitScript(
      ({ authKey, locale, theme }) => {
        window.sessionStorage.setItem('gpt-load.auth-key', authKey)
        window.localStorage.setItem('gpt-load.locale', locale)
        window.localStorage.setItem('gpt-load.theme', theme)
      },
      { authKey: visualAuthKey, locale: item.locale, theme: item.theme },
    )
    const api = await installVisualApi(page, item.scenario)

    await page.goto(item.path)
    await expect(page.locator('main h1')).toHaveCount(1)
    await prepareScenario(page, item)
    await expect(page.locator('.query-feedback--loading')).toHaveCount(0)
    await page.evaluate(async () => {
      window.scrollTo(0, 0)
      await document.fonts.ready
      await new Promise<void>((resolveFrame) => requestAnimationFrame(() => resolveFrame()))
      await new Promise<void>((resolveFrame) => requestAnimationFrame(() => resolveFrame()))
    })
    expect(api.unexpectedRequests).toEqual([])

    captures.push(await captureVisualScenario(page, testInfo, item))
  })
}
