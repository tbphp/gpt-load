import { expect, test } from '@playwright/test'

const authKey = 'e2e-auth-canary'
const firstUpstreamKey = 'e2e-upstream-key-one'
const secondUpstreamKey = 'e2e-upstream-key-one-secondary'
const groupName = 'E2E OpenAI Group'
const accessKeyName =
  'E2EAccessKeyWithAnIntentionallyLongUnbrokenNameForMobileViewportOverflowRegressionCoverage'
const upstreamURL = 'http://127.0.0.1:3108'
const discoveredModel = 'e2e-model-one'
const secondDiscoveredModel = 'e2e-model-two'
const rpmLimit = '37'
const expectedUsageRequests = 3

test.use({ locale: 'en-US' })
test.setTimeout(120_000)

test('critical management journey works through the embedded binary', async ({ page }) => {
  const browserUpstreamRequests: string[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.hostname === '127.0.0.1' && url.port === '3108') {
      browserUpstreamRequests.push(`${request.method()} ${url.pathname}`)
    }
  })

  let groupID = ''
  let issuedAccessKey = ''

  await test.step('login', async () => {
    await page.goto('/login')
    await page.getByLabel('AUTH_KEY', { exact: true }).fill(authKey)
    await page.getByRole('button', { name: 'Sign in' }).click()

    await expect(page).toHaveURL('/')
    await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible()
  })

  await test.step('discover and create Group', async () => {
    await page.goto('/import')
    await page.getByLabel('Channel preset').selectOption('custom')
    await page.getByLabel('Group name (optional)').fill(groupName)
    await page.getByLabel('Upstream URL').fill(upstreamURL)
    await page.getByRole('group', { name: 'Protocols' }).getByLabel('openai').check()
    await page.getByLabel('Upstream keys').fill(firstUpstreamKey)
    await page.getByRole('button', { name: 'Discover models' }).click()

    await expect(page.getByRole('checkbox', { name: discoveredModel })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: secondDiscoveredModel })).toBeChecked()
    await page.getByRole('button', { name: 'Review' }).click()
    await expect(page.getByText(discoveredModel, { exact: false })).toBeVisible()
    await expect(page.getByText(secondDiscoveredModel, { exact: false })).toBeVisible()
    await page.getByRole('button', { name: 'Create Group' }).click()

    await expect(page).toHaveURL(/\/groups\/\d+\?tab=keys$/)
    groupID = new URL(page.url()).pathname.split('/').at(-1) ?? ''
    expect(groupID).toMatch(/^\d+$/)
    await expect(page.getByRole('heading', { name: groupName })).toBeVisible()
  })

  await test.step('append keys to existing Group', async () => {
    await page.getByRole('link', { name: 'Import keys' }).click()
    await expect(page).toHaveURL(`/import?mode=existing&group_id=${groupID}`)
    await expect(page.getByLabel('Destination Group')).toHaveValue(groupID)
    await page.getByLabel('Upstream keys').fill(secondUpstreamKey)
    await page.getByRole('button', { name: 'Review' }).click()
    await page.getByRole('button', { name: 'Import keys' }).click()

    await expect(page.getByRole('status')).toContainText('1 added; 0 already existed.')
    await page.goto(`/groups/${groupID}?tab=models`)
    await expect(page.getByRole('checkbox', { name: discoveredModel })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: secondDiscoveredModel })).toBeChecked()
  })

  await test.step('configure AccessKey filters and rpm', async () => {
    await page.goto('/access-keys')
    await page.getByRole('button', { name: 'Create AccessKey' }).click()

    const createDrawer = page.getByRole('dialog', { name: 'Create AccessKey' })
    await createDrawer.getByLabel('Name', { exact: true }).fill(accessKeyName)
    await createDrawer.getByRole('group', { name: 'Group filters' }).getByLabel(groupName).check()
    await createDrawer
      .getByRole('group', { name: 'Protocol filters' })
      .getByLabel('OpenAI', { exact: true })
      .check()
    await createDrawer.getByPlaceholder('Enter a model ID or alias').fill(discoveredModel)
    await createDrawer.getByRole('button', { name: 'Add model' }).click()
    await createDrawer.getByLabel('Requests per minute').fill(rpmLimit)
    await createDrawer.getByRole('button', { name: 'Save AccessKey' }).click()
    await expect(createDrawer.getByText('Plaintext AccessKey', { exact: true })).toBeVisible()
    await createDrawer.locator('[data-test="access-key-result-reveal"]').click({ timeout: 10_000 })
    issuedAccessKey = await createDrawer.locator('.access-key-drawer__secret code').innerText()
    expect(issuedAccessKey).toMatch(/^sk-gl-[0-9a-f]{32}$/)
    await createDrawer.getByRole('button', { name: 'Close AccessKey editor' }).click()

    const accessKeyRow = page.getByRole('row').filter({
      has: page.getByText(accessKeyName, { exact: true }),
    })
    await expect(accessKeyRow).toContainText(groupName)
    const protocolFilter = accessKeyRow.locator('dl > div', {
      has: page.getByText('Protocols', { exact: true }),
    })
    await expect(protocolFilter.locator('dd')).toHaveText('OpenAI')
    await expect(accessKeyRow).toContainText(discoveredModel)
    await expect(accessKeyRow).toContainText(rpmLimit)
    await accessKeyRow.getByRole('button', { name: 'Edit' }).click()

    const editDrawer = page.getByRole('dialog', { name: 'Edit AccessKey' })
    await expect(editDrawer.getByLabel('Name', { exact: true })).toHaveValue(accessKeyName)
    await expect(
      editDrawer.getByRole('group', { name: 'Group filters' }).getByLabel(groupName),
    ).toBeChecked()
    await expect(
      editDrawer
        .getByRole('group', { name: 'Protocol filters' })
        .getByLabel('OpenAI', { exact: true }),
    ).toBeChecked()
    await expect(
      editDrawer.getByRole('button', { name: `Remove model ${discoveredModel}` }),
    ).toBeVisible()
    await expect(editDrawer.getByLabel('Requests per minute')).toHaveValue(rpmLimit)
    await editDrawer.getByRole('button', { name: 'Close AccessKey editor' }).click()
  })

  await test.step('manage a global price override through the canonical deep link', async () => {
    await page.goto('/settings/model-prices')
    await expect(page).toHaveURL('/settings/model-prices')
    await expect(page.getByRole('heading', { name: 'Model prices' })).toBeVisible()

    const addPrice = page.locator('[data-test="model-price-add"]')
    await addPrice.click()
    const addDrawer = page.getByRole('dialog', { name: 'Add model price override' })
    await addDrawer.getByLabel('Model pattern').fill('*')
    await addDrawer.getByLabel('Uncached input').fill('1')
    await expect(addDrawer.getByRole('button', { name: 'Save override' })).toBeDisabled()
    await addDrawer.getByLabel('I understand that * shadows every built-in price rule.').check()
    await addDrawer.getByRole('button', { name: 'Save override' }).click()

    const overrideRow = page.locator('[data-test="override-price-row-0"]')
    await expect(overrideRow).toContainText('*')
    await expect(overrideRow).toContainText('$1')

    const editPrice = page.locator('[data-test="override-price-edit-0"]')
    await editPrice.click()
    const editDrawer = page.getByRole('dialog', { name: 'Edit model price override' })
    await expect(editDrawer.getByLabel('Model pattern')).toHaveAttribute('readonly', '')
    await editDrawer.getByLabel('Output').fill('2')
    await editDrawer.getByLabel('I understand that * shadows every built-in price rule.').check()
    await editDrawer.getByRole('button', { name: 'Save override' }).click()
    await expect(editPrice).toBeFocused()
    await expect(page.locator('[data-test="override-price-row-0"]')).toContainText('$2')

    const resetPrice = page.locator('[data-test="model-price-reset-open"]')
    await resetPrice.click()
    const resetDialog = page.getByRole('dialog', {
      name: 'Reset this model price override?',
    })
    await resetDialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(resetPrice).toBeFocused()
    await resetPrice.click()
    await resetDialog.getByRole('button', { name: 'Reset override' }).click()
    await expect(page.getByText('No overrides configured', { exact: true })).toBeVisible()
  })

  await test.step('produce persisted complete, partial, and missing usage safely', async () => {
    for (const [content, stream] of [
      ['missing-usage', false],
      ['partial-usage', true],
      ['complete-usage', false],
    ] as const) {
      const response = await page.request.post('/v1/chat/completions', {
        headers: {
          Authorization: `Bearer ${issuedAccessKey}`,
          'Content-Type': 'application/json',
        },
        data: {
          model: discoveredModel,
          messages: [{ role: 'user', content }],
          stream,
        },
      })
      expect(response.status()).toBe(200)
    }

    await expect
      .poll(
        async () => {
          const response = await page.request.get('/api/logs', {
            headers: { Authorization: `Bearer ${authKey}` },
          })
          if (!response.ok()) return -1
          const payload = (await response.json()) as { data?: { items?: unknown[] } }
          return payload.data?.items?.length ?? -1
        },
        { message: 'wait for all request logs to persist' },
      )
      .toBe(expectedUsageRequests)

    await expect
      .poll(
        async () => {
          const response = await page.request.get('/api/usage?range=24h', {
            headers: { Authorization: `Bearer ${authKey}` },
          })
          if (!response.ok()) return { requestCount: -1, status: response.status() }
          const payload = (await response.json()) as {
            data?: { summary?: { request_count?: number } }
          }
          return {
            requestCount: payload.data?.summary?.request_count ?? -1,
            status: response.status(),
          }
        },
        { message: 'wait for the usage aggregate to include all persisted requests' },
      )
      .toEqual({ requestCount: expectedUsageRequests, status: 200 })
  })

  await test.step('review usage quality, request detail usage, and the Home summary', async () => {
    await page.goto('/monitor?tab=usage&range=24h')
    await expect(page).toHaveURL('/monitor?tab=usage&range=24h')
    await expect(page.locator('[data-test="usage-quality-missing"]')).toContainText('1')
    await expect(page.locator('[data-test="usage-quality-partial"]')).toContainText('1')
    await expect(page.locator('[data-test="usage-quality-unpriced"]')).toContainText('3')
    await expect(page.locator('[data-test="usage-kpi-cost"]')).toContainText('Unknown')
    await expect(page.locator('[data-test="usage-kpi-cost"]')).not.toContainText('$0')
    await expect(page.locator('[data-test="usage-kpi-cost"]')).not.toContainText('Free')
    await expect(page.locator('[data-test="usage-scope"]')).toContainText('current process')
    await expect(page.locator('[data-test="usage-prices-link"]')).toHaveAttribute(
      'href',
      '/settings/model-prices',
    )

    await page.goto('/monitor?tab=logs')
    const firstLogRow = page.locator('[data-test^="log-row-"]').first()
    await expect(firstLogRow).toBeVisible()
    await firstLogRow.getByRole('button', { name: 'View details' }).click()
    const logDrawer = page.getByRole('dialog', { name: 'Request log details' })
    const logUsage = logDrawer.locator('[data-test="log-usage-cost"]')
    await expect(logUsage).toContainText('Complete usage')
    await expect(logUsage).toContainText('Estimated cost unknown')
    await expect(logUsage).toContainText('Uncached input')
    await expect(logUsage).toContainText('11')
    await expect(logDrawer.locator('[data-test="log-usage-prices-link"]')).toHaveAttribute(
      'href',
      '/settings/model-prices',
    )
    await logDrawer.getByRole('button', { name: 'Close request log details' }).click()

    await page.goto('/')
    await expect(page.locator('[data-test="home-usage-requests"]')).toContainText(
      String(expectedUsageRequests),
    )
    await expect(page.locator('[data-test="home-usage-cost"]')).toContainText('Unknown')
    await expect(page.locator('[data-test="home-usage-tokens"]')).toContainText('Unknown')
    await expect(page.locator('[data-test="home-usage-tokens"]')).not.toContainText('0')
    await expect(page.locator('[data-test="home-usage-quality-missing"]')).toContainText(
      'Usage missing 1',
    )
    await expect(page.locator('[data-test="home-usage-quality-partial"]')).toContainText(
      'Usage partial 1',
    )
    await expect(page.locator('[data-test="home-usage-quality-unpriced"]')).toContainText(
      'Cost unpriced 3',
    )
    await expect(page.locator('[data-test="home-usage-detail"]')).toHaveAttribute(
      'href',
      '/monitor?tab=usage&range=24h',
    )

    const visibleText = await page.locator('body').innerText()
    for (const secret of [authKey, firstUpstreamKey, secondUpstreamKey, issuedAccessKey]) {
      expect(visibleText).not.toContain(secret)
    }
  })

  await test.step('verify responsive light and dark layouts with table-local overflow', async () => {
    const viewports = [
      { width: 375, height: 812 },
      { width: 768, height: 900 },
      { width: 1024, height: 900 },
      { width: 1440, height: 900 },
    ] as const

    for (const theme of ['light', 'dark'] as const) {
      await page.evaluate((value) => localStorage.setItem('gpt-load.theme', value), theme)
      for (const viewport of viewports) {
        await page.setViewportSize(viewport)
        await page.goto('/')
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
        await expect(page.locator('[data-test="home-usage-requests"]')).toBeVisible()

        const layout = await page.evaluate(() => {
          const overview = document.querySelector('.home-overview')?.getBoundingClientRect()
          const usage = document.querySelector('.usage-summary-section')?.getBoundingClientRect()
          const groups = document.querySelector('.groups-section')?.getBoundingClientRect()
          return {
            documentWidth: document.documentElement.scrollWidth,
            overviewBottom: overview?.bottom ?? -1,
            usageBottom: usage?.bottom ?? -1,
            usageTop: usage?.top ?? -1,
            groupsTop: groups?.top ?? -1,
            viewportWidth: window.innerWidth,
          }
        })
        expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth)
        expect(layout.usageTop).toBeGreaterThanOrEqual(layout.overviewBottom)
        expect(layout.groupsTop).toBeGreaterThanOrEqual(layout.usageBottom)
      }
    }

    await page.setViewportSize({ width: 375, height: 812 })
    await page.goto('/settings/model-prices')
    const priceTable = page.locator('.data-table__container').first()
    await expect(priceTable).toBeVisible()
    const overflow = await priceTable.evaluate((table) => ({
      client: table.clientWidth,
      scroll: table.scrollWidth,
      document: document.documentElement.scrollWidth,
      viewport: window.innerWidth,
    }))
    expect(overflow.scroll).toBeGreaterThan(overflow.client)
    expect(overflow.document).toBeLessThanOrEqual(overflow.viewport)
  })

  await test.step('inspect current route', async () => {
    await page.setViewportSize({ width: 375, height: 812 })
    await page.goto('/monitor?tab=inspector')
    const upstreamRequestCountBeforeInspect = browserUpstreamRequests.length
    expect(upstreamRequestCountBeforeInspect).toBe(0)

    await page.getByLabel('Protocol', { exact: true }).click()
    await page.getByRole('option', { name: 'OpenAI', exact: true }).click()
    await page.getByLabel('Client model').fill(discoveredModel)
    const accessKeyTrigger = page.getByLabel('AccessKey', { exact: true })
    await accessKeyTrigger.click()
    const accessKeyOption = page.getByRole('option', {
      name: new RegExp(`^${accessKeyName} · #\\d+ · Active$`),
    })
    const listboxBounds = await page.getByRole('listbox').boundingBox()
    expect(listboxBounds).not.toBeNull()
    expect(listboxBounds?.x).toBeGreaterThanOrEqual(0)
    expect((listboxBounds?.x ?? 0) + (listboxBounds?.width ?? 0)).toBeLessThanOrEqual(
      await page.evaluate(() => window.innerWidth),
    )
    await accessKeyOption.click()
    const pageWidths = await page.evaluate(() => ({
      client: document.documentElement.clientWidth,
      scroll: document.documentElement.scrollWidth,
      viewport: window.innerWidth,
    }))
    expect(pageWidths.scroll).toBeLessThanOrEqual(pageWidths.viewport)
    expect(pageWidths.client).toBe(pageWidths.viewport)
    const triggerWidths = await accessKeyTrigger.evaluate((trigger) => ({
      client: trigger.clientWidth,
      scroll: trigger.scrollWidth,
    }))
    expect(triggerWidths.scroll).toBeLessThanOrEqual(triggerWidths.client)

    const routeInspectRequestPromise = page.waitForRequest((request) => {
      const url = new URL(request.url())
      return request.method() === 'POST' && url.pathname === '/api/route/inspect'
    })
    await page.getByRole('button', { name: 'Inspect current route' }).click()
    const routeInspectRequest = await routeInspectRequestPromise
    expect(routeInspectRequest.postDataJSON()).toMatchObject({
      protocol: 'openai',
      external_model: discoveredModel,
    })

    const result = page.locator('[data-test="inspector-result"]')
    await expect(result.locator('[data-test="inspector-result-state"]')).toHaveText('Routable')
    await expect(result.locator('[data-test="inspector-result-reason"]')).toHaveText(
      'No exclusion reason',
    )
    await expect(result).toContainText(groupName)
    await expect(result).toContainText(discoveredModel)
    expect(browserUpstreamRequests).toHaveLength(upstreamRequestCountBeforeInspect)
  })
})
