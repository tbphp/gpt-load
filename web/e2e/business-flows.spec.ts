import { expect, test } from './fixtures'
import {
  visualFixtureData,
  visualLocales,
  visualScenarioLabel,
  visualThemes,
  visualViewports,
} from './visual-fixtures'

const authKey = process.env.GPT_LOAD_E2E_AUTH_KEY
const upstreamURL = process.env.GPT_LOAD_E2E_UPSTREAM_URL
if (!authKey || !upstreamURL) throw new Error('E2E harness environment is incomplete')
const firstUpstreamKey = 'e2e-upstream-key-one'
const secondUpstreamKey = 'e2e-upstream-key-one-secondary'
const groupName = visualFixtureData.groupName
const accessKeyName = visualFixtureData.longIdentifier
const upstreamOrigin = new URL(upstreamURL).origin
const discoveredModel = 'e2e-model-one'
const secondDiscoveredModel = 'e2e-model-two'
const rpmLimit = '37'
const expectedUsageRequests = 3

test.use({ locale: 'en-US' })
test.setTimeout(120_000)

test('critical management journey works through the embedded binary', async ({ page }) => {
  const browserUpstreamRequests: string[] = []
  const settingsPutBodies: unknown[] = []
  const usageRequests: string[] = []
  const logRequests: string[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.origin === upstreamOrigin) {
      browserUpstreamRequests.push(`${request.method()} ${url.pathname}`)
    }
    if (request.method() === 'PUT' && url.pathname === '/api/settings') {
      settingsPutBodies.push(request.postDataJSON())
    }
    if (request.method() === 'GET' && url.pathname === '/api/usage') {
      usageRequests.push(`${url.pathname}${url.search}`)
    }
    if (request.method() === 'GET' && url.pathname === '/api/logs') {
      logRequests.push(`${url.pathname}${url.search}`)
    }
  })

  let groupID = ''
  let issuedAccessKey = ''

  await test.step(visualScenarioLabel('home-normal'), async () => {
    await page.goto('/login')
    await page.getByLabel('AUTH_KEY', { exact: true }).fill(authKey)
    await page.getByRole('button', { name: 'Sign in' }).click()

    await expect(page).toHaveURL('/')
    await expect(page.getByRole('navigation', { name: 'Primary navigation' })).toBeVisible()
  })

  await test.step('focus and announce SPA navigation', async () => {
    await page
      .getByRole('navigation', { name: 'Primary navigation' })
      .getByRole('link', { name: 'Monitor' })
      .click()

    await expect(page).toHaveURL('/monitor?tab=health')
    const heading = page.getByRole('heading', { level: 1, name: 'Monitor' })
    await expect(heading).toBeFocused()
    await expect(page.locator('[data-test="route-announcer"]')).toHaveText('Monitor')
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

  await test.step(visualScenarioLabel('access-keys-long'), async () => {
    await page.goto('/access-keys')
    await page.getByRole('button', { name: 'Create AccessKey' }).click()

    const createDrawer = page.getByRole('dialog', { name: 'Create AccessKey' })
    await createDrawer.getByLabel('Name', { exact: true }).fill(accessKeyName)
    await createDrawer.locator('[data-test="access-key-groups-mode"]').selectOption('restricted')
    await createDrawer.getByRole('group', { name: 'Group filters' }).getByLabel(groupName).check()
    await createDrawer.locator('[data-test="access-key-protocols-mode"]').selectOption('restricted')
    await createDrawer
      .getByRole('group', { name: 'Protocol filters' })
      .getByLabel('OpenAI', { exact: true })
      .check()
    await createDrawer.locator('[data-test="access-key-models-mode"]').selectOption('restricted')
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
    await editDrawer.getByLabel('Name', { exact: true }).fill(`${accessKeyName} draft`)

    const closeEditor = editDrawer.getByRole('button', { name: 'Close AccessKey editor' })
    const rejectedDialogPromise = page.waitForEvent('dialog')
    const rejectedClosePromise = closeEditor.click()
    const rejectedDialog = await rejectedDialogPromise
    expect(rejectedDialog.type()).toBe('confirm')
    await rejectedDialog.dismiss()
    await rejectedClosePromise
    await expect(editDrawer).toBeVisible()
    await expect(editDrawer.getByLabel('Name', { exact: true })).toHaveValue(
      `${accessKeyName} draft`,
    )

    const acceptedDialogPromise = page.waitForEvent('dialog')
    const acceptedClosePromise = closeEditor.click()
    const acceptedDialog = await acceptedDialogPromise
    expect(acceptedDialog.type()).toBe('confirm')
    await acceptedDialog.accept()
    await acceptedClosePromise
    await expect(editDrawer).toBeHidden()
    await expect(accessKeyRow).toContainText(accessKeyName)
    await expect(accessKeyRow).not.toContainText(`${accessKeyName} draft`)
  })

  await test.step(visualScenarioLabel('model-prices-mixed'), async () => {
    await page.goto('/settings/model-prices')
    await expect(page).toHaveURL('/settings/model-prices')
    await expect(page.getByRole('heading', { name: 'Model prices' })).toBeVisible()

    const addPrice = page.locator('[data-test="model-price-add"]')
    await addPrice.click()
    const addDrawer = page.getByRole('dialog', { name: 'Add model price override' })
    await addDrawer.getByLabel('Model pattern').fill('*')
    await addDrawer.getByLabel('Uncached input').fill('1')
    await expect(addDrawer).toContainText('* shadows every built-in price rule')
    await expect(addDrawer.getByRole('button', { name: 'Save override' })).toBeEnabled()
    await addDrawer.getByRole('button', { name: 'Save override' }).click()

    await expect(page.getByRole('dialog')).toHaveCount(1)
    const globalPriceConfirmation = addDrawer.locator('[data-test="model-price-global-confirm"]')
    await expect(globalPriceConfirmation).toContainText(
      'takes precedence over every built-in exact and prefix rule',
    )
    await expect(globalPriceConfirmation).toContainText('Unset price slots do not fall back')
    await expect(globalPriceConfirmation).toContainText('future completed or Emit requests')
    await expect(globalPriceConfirmation).toContainText(
      'Reset restores the remaining user and built-in rules',
    )
    await expect(
      globalPriceConfirmation.locator('[data-test="model-price-global-confirm-heading"]'),
    ).toBeFocused()
    await globalPriceConfirmation.getByRole('button', { name: 'Create global override' }).click()

    const overrideRow = page.locator('[data-test="override-price-row-0"]')
    await expect(overrideRow).toContainText('*')
    await expect(overrideRow).toContainText('$1')

    const editPrice = page.locator('[data-test="override-price-edit-0"]')
    await editPrice.click()
    const editDrawer = page.getByRole('dialog', { name: 'Edit model price override' })
    await expect(editDrawer.getByLabel('Model pattern')).toHaveAttribute('readonly', '')
    await editDrawer.getByLabel('Output').fill('2')
    await editDrawer.getByRole('button', { name: 'Save override' }).click()
    await expect(page.getByRole('dialog')).toHaveCount(1)
    const editGlobalPriceConfirmation = editDrawer.locator(
      '[data-test="model-price-global-confirm"]',
    )
    await expect(
      editGlobalPriceConfirmation.locator('[data-test="model-price-global-confirm-heading"]'),
    ).toBeFocused()
    await editGlobalPriceConfirmation
      .getByRole('button', { name: 'Create global override' })
      .click()
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

  await test.step(visualScenarioLabel('settings-dirty'), async () => {
    await page.goto('/settings')
    await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible()

    await page.locator('[data-test="override-request_timeout"]').check()
    await page.locator('[data-test="value-request_timeout"]').fill('901')
    await page.locator('[data-test="override-request_log_retention_days"]').check()
    await page.locator('[data-test="value-request_log_retention_days"]').fill('31')
    await expect(page.locator('[data-test="settings-dirty-summary"]')).toContainText(
      '2 unsaved runtime settings',
    )

    const putCountBefore = settingsPutBodies.length
    const settingsPut = page.waitForRequest((request) => {
      const url = new URL(request.url())
      return request.method() === 'PUT' && url.pathname === '/api/settings'
    })
    await page.locator('[data-test="settings-save-all"]').click()
    const request = await settingsPut

    expect(request.postDataJSON()).toEqual({
      settings: {
        request_timeout: 901,
        request_log_retention_days: 31,
      },
    })
    await expect(page.locator('[data-test="settings-saved-at"]')).toBeVisible()
    expect(settingsPutBodies.slice(putCountBefore)).toHaveLength(1)
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

  await test.step(
    [
      visualScenarioLabel('usage-quality'),
      visualScenarioLabel('logs-signal-path'),
      visualScenarioLabel('home-anomaly'),
    ].join(' · '),
    async () => {
      await page.goto('/monitor?tab=usage&range=24h')
      await expect(page).toHaveURL('/monitor?tab=usage&range=24h')
      await expect(page.locator('[data-test="usage-freshness"]')).toBeVisible()
      const usageRequestCountBeforeDraft = usageRequests.length
      const usageApplied = page.locator('[data-test="usage-applied-filters"]')
      await expect(usageApplied).toContainText('Last 24 hours')
      await expect(usageApplied).not.toContainText(discoveredModel)

      await page.locator('[data-test="usage-range"]').selectOption('30d')
      await page.locator('[data-test="usage-group"]').selectOption(groupID)
      await page.locator('[data-test="usage-model"]').fill(discoveredModel)
      await expect(page).toHaveURL('/monitor?tab=usage&range=24h')
      expect(usageRequests).toHaveLength(usageRequestCountBeforeDraft)
      await expect(usageApplied).toContainText('Last 24 hours')
      await expect(usageApplied).not.toContainText(discoveredModel)

      await page.getByRole('button', { name: 'Apply' }).click()
      await expect(page).toHaveURL(
        `/monitor?tab=usage&range=30d&group_id=${groupID}&model=${discoveredModel}`,
      )
      await expect.poll(() => usageRequests.length).toBe(usageRequestCountBeforeDraft + 1)
      await expect(usageApplied).toContainText('Last 30 days')
      await expect(usageApplied).toContainText(groupName)
      await expect(usageApplied).toContainText(discoveredModel)

      const usageRequestCountBeforeReset = usageRequests.length
      await page.locator('[data-test="usage-reset"]').click()
      await expect(page).toHaveURL(
        `/monitor?tab=usage&range=30d&group_id=${groupID}&model=${discoveredModel}`,
      )
      expect(usageRequests).toHaveLength(usageRequestCountBeforeReset)
      await expect(page.locator('[data-test="usage-range"]')).toHaveValue('24h')
      await expect(page.locator('[data-test="usage-group"]')).toHaveValue('')
      await expect(page.locator('[data-test="usage-model"]')).toHaveValue('')
      await expect(usageApplied).toContainText(discoveredModel)

      await page.getByRole('button', { name: 'Apply' }).click()
      await expect(page).toHaveURL('/monitor?tab=usage&range=24h')
      await expect.poll(() => usageRequests.length).toBe(usageRequestCountBeforeReset + 1)
      await expect(page.locator('[data-test="usage-quality-missing"]')).toContainText('1')
      await expect(page.locator('[data-test="usage-quality-partial"]')).toContainText('1')
      await expect(page.locator('[data-test="usage-quality-unpriced"]')).toContainText('3')
      await expect(page.locator('[data-test="usage-kpi-cost"]')).toContainText('$0.00 + unknown')
      await expect(page.locator('[data-test="usage-kpi-cost"]')).not.toContainText('Free')
      await expect(page.locator('[data-test="usage-scope"]')).toContainText('current process')
      await expect(page.locator('[data-test="usage-prices-link"]')).toHaveAttribute(
        'href',
        '/settings/model-prices',
      )

      await page.goto('/monitor?tab=logs')
      const firstLogRow = page.locator('[data-test^="log-row-"]').first()
      await expect(firstLogRow).toBeVisible()
      const logRequestCountBeforeDraft = logRequests.length
      const logsApplied = page.locator('[data-test="logs-applied-filters"]')
      await expect(logsApplied).toContainText('No filters')
      await page.locator('[data-test="logs-status"]').selectOption('error')
      await expect(page).toHaveURL('/monitor?tab=logs')
      expect(logRequests).toHaveLength(logRequestCountBeforeDraft)
      await expect(logsApplied).not.toContainText('Status Error')

      await page.locator('[data-test="logs-reset"]').click()
      await expect(page.locator('[data-test="logs-status"]')).toHaveValue('')
      await expect(page).toHaveURL('/monitor?tab=logs')
      expect(logRequests).toHaveLength(logRequestCountBeforeDraft)
      await expect(logsApplied).toContainText('No filters')

      await page.locator('[data-test="logs-status"]').selectOption('success')
      await page.getByRole('button', { name: 'Apply' }).click()
      await expect(page).toHaveURL('/monitor?tab=logs&status=success')
      await expect.poll(() => logRequests.length).toBe(logRequestCountBeforeDraft + 1)
      await expect(logsApplied).toContainText('Status Success')

      const firstLogTestID = await firstLogRow.getAttribute('data-test')
      const firstLogID = firstLogTestID?.replace('log-row-', '') ?? ''
      expect(firstLogID).toMatch(/^[0-9a-f-]{36}$/)
      const logRequestCountBeforeSelection = logRequests.length
      await firstLogRow.getByRole('button', { name: 'View details' }).click()
      const selectedLogsURL = `/monitor?tab=logs&status=success&selected_request_id=${firstLogID}`
      await expect(page).toHaveURL(selectedLogsURL)
      expect(logRequests).toHaveLength(logRequestCountBeforeSelection)

      const logDrawer = page.getByRole('dialog', { name: 'Request log details' })
      await logDrawer.locator('[data-test="log-inspector-link"]').click()
      await expect(page).toHaveURL(/\/monitor\?tab=inspector&protocol=openai/)
      await page.goBack()
      await expect(page).toHaveURL(selectedLogsURL)
      await expect(logDrawer).toBeVisible()

      const logUsage = logDrawer.locator('[data-test="log-usage-cost"]')
      await expect(logUsage).toContainText('Complete usage')
      await expect(logUsage).toContainText('Estimated cost unknown')
      await expect(logUsage).toContainText('Uncached input')
      await expect(logUsage).toContainText('11')
      await expect(logDrawer.locator('[data-test="log-usage-prices-link"]')).toHaveAttribute(
        'href',
        '/settings/model-prices',
      )
      const logDetailTrigger = page.locator(`[data-test="log-details-${firstLogID}"]`)
      await logDrawer.getByRole('button', { name: 'Close request log details' }).click()
      await expect(page).toHaveURL('/monitor?tab=logs&status=success')
      await expect(logDetailTrigger).toBeFocused()

      await page.goto('/')
      await expect(page.locator('[data-test="home-usage-requests"]')).toContainText(
        String(expectedUsageRequests),
      )
      await expect(page.locator('[data-test="home-usage-cost"]')).toContainText('$0.00 + unknown')
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
    },
  )

  await test.step('verify responsive light and dark layouts with table-local overflow', async () => {
    for (const locale of visualLocales) {
      await page.evaluate((value) => localStorage.setItem('gpt-load.locale', value), locale)
      for (const theme of visualThemes) {
        await page.evaluate((value) => localStorage.setItem('gpt-load.theme', value), theme)
        for (const viewport of visualViewports) {
          await page.setViewportSize(viewport)
          await page.goto('/')
          await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
          await expect(page.locator('[data-test="home-usage-requests"]')).toBeVisible()
          await expect(page.locator('[data-test="connection-snippet"]')).toContainText(
            'GPT_LOAD_API_KEY',
          )

          const layout = await page.evaluate(() => {
            const topbar = document.querySelector('.app-topbar')?.getBoundingClientRect()
            const main = document.querySelector('#main-content')?.getBoundingClientRect()
            const operational = document
              .querySelector('[data-test="home-operational-overview"]')
              ?.getBoundingClientRect()
            const overview = document.querySelector('.home-overview')?.getBoundingClientRect()
            const usage = document.querySelector('.usage-summary-section')?.getBoundingClientRect()
            const groups = document.querySelector('.groups-section')?.getBoundingClientRect()
            const connection = document
              .querySelector('[data-test="home-connection"]')
              ?.getBoundingClientRect()
            return {
              documentWidth: document.documentElement.scrollWidth,
              operationalBottom: operational?.bottom ?? -1,
              operationalTop: operational?.top ?? -1,
              overviewBottom: overview?.bottom ?? -1,
              overviewTop: overview?.top ?? -1,
              usageBottom: usage?.bottom ?? -1,
              usageTop: usage?.top ?? -1,
              groupsBottom: groups?.bottom ?? -1,
              groupsTop: groups?.top ?? -1,
              connectionTop: connection?.top ?? -1,
              mainTop: main?.top ?? -1,
              topbarBottom: topbar?.bottom ?? -1,
              topbarHeight: topbar?.height ?? -1,
              viewportWidth: window.innerWidth,
            }
          })
          expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth)
          expect(layout.mainTop).toBeGreaterThanOrEqual(layout.topbarBottom)
          expect(layout.topbarHeight).toBeLessThanOrEqual(viewport.width < 640 ? 132 : 80)
          expect(layout.overviewTop).toBeGreaterThanOrEqual(layout.operationalTop)
          expect(layout.usageTop).toBeGreaterThanOrEqual(layout.overviewTop)
          expect(layout.usageBottom).toBeLessThanOrEqual(layout.overviewBottom)
          expect(layout.groupsTop).toBeGreaterThanOrEqual(layout.operationalBottom)
          expect(layout.connectionTop).toBeGreaterThanOrEqual(layout.groupsBottom)
        }
      }
    }

    await page.evaluate(() => localStorage.setItem('gpt-load.locale', 'en-US'))
    await page.reload()
    const preferences = page.getByRole('button', { name: 'Preferences' })
    await preferences.click()
    await expect(page.locator('[data-test="preferences-panel"]')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(preferences).toBeFocused()

    const protocol = page.getByLabel('Protocol', { exact: true })
    await protocol.click()
    await page.getByRole('option', { name: 'Anthropic' }).click()
    await expect(page.locator('[data-test="connection-snippet"]')).toContainText('/v1/messages')
    await expect(page.locator('[data-test="connection-snippet"]')).toContainText(
      'anthropic-version',
    )
    await protocol.click()
    await page.getByRole('option', { name: 'Gemini' }).click()
    await expect(page.locator('[data-test="connection-snippet"]')).toContainText('/v1beta/models/')
    await expect(page.locator('[data-test="connection-snippet"]')).toContainText('x-goog-api-key')

    await page.setViewportSize({ width: 375, height: 812 })
    await page.goto('/settings/model-prices')
    await expect(page.locator('[data-test^="builtin-price-card-"]').first()).toBeVisible()
    await expect(page.locator('[data-test^="builtin-price-row-"]')).toHaveCount(0)

    await page.goto('/access-keys')
    await expect(page.locator('[data-test^="access-key-card-"]').first()).toBeVisible()
    await expect(page.locator('[data-test^="access-key-row-"]')).toHaveCount(0)

    await page.setViewportSize({ width: 768, height: 900 })
    await page.goto('/settings/model-prices')
    await expect(page.locator('[data-test^="builtin-price-row-"]').first()).toBeVisible()
    await expect(page.locator('[data-test^="builtin-price-card-"]')).toHaveCount(0)
    const priceTable = page.locator('.data-table__container').first()
    await expect(priceTable).toBeVisible()
    await expect(priceTable).toHaveAttribute('tabindex', '0')
    await priceTable.focus()
    await expect(priceTable).toBeFocused()
    const overflow = await priceTable.evaluate((table) => ({
      client: table.clientWidth,
      scroll: table.scrollWidth,
      document: document.documentElement.scrollWidth,
      viewport: window.innerWidth,
    }))
    expect(overflow.scroll).toBeGreaterThan(overflow.client)
    expect(overflow.document).toBeLessThanOrEqual(overflow.viewport)

    await page.goto('/access-keys')
    await expect(page.locator('[data-test^="access-key-row-"]').first()).toBeVisible()
    await expect(page.locator('[data-test^="access-key-card-"]')).toHaveCount(0)

    await page.setViewportSize({ width: 1024, height: 900 })
    await page.goto('/settings/model-prices')
    await expect(page.locator('[data-test^="builtin-price-row-"]').first()).toBeVisible()
    await expect(page.locator('[data-test^="builtin-price-card-"]')).toHaveCount(0)
    await page.goto('/access-keys')
    await expect(page.locator('[data-test^="access-key-row-"]').first()).toBeVisible()
    await expect(page.locator('[data-test^="access-key-card-"]')).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
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

  await test.step('recover from an unknown route', async () => {
    await page.goto('/phase-2-unknown-route')
    await expect(page).toHaveURL('/phase-2-unknown-route')
    await expect(page.getByRole('heading', { level: 1, name: 'Page not found' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Back to Home' })).toHaveAttribute('href', '/')
  })
})
