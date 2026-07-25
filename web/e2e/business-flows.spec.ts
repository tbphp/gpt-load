import { expect, test } from '@playwright/test'

const authKey = 'e2e-auth-canary'
const firstUpstreamKey = 'e2e-upstream-key-one'
const secondUpstreamKey = 'e2e-upstream-key-one-secondary'
const groupName = 'E2E OpenAI Group'
const accessKeyName = 'E2E filtered client'
const upstreamURL = 'http://127.0.0.1:3108'
const discoveredModel = 'e2e-model-one'
const secondDiscoveredModel = 'e2e-model-two'
const rpmLimit = '37'

test.use({ locale: 'en-US' })

test('critical management journey works through the embedded binary', async ({ page }) => {
  const browserUpstreamRequests: string[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.hostname === '127.0.0.1' && url.port === '3108') {
      browserUpstreamRequests.push(`${request.method()} ${url.pathname}`)
    }
  })

  let groupID = ''

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

  await test.step('inspect current route', async () => {
    await page.goto('/monitor?tab=inspector')
    const upstreamRequestCountBeforeInspect = browserUpstreamRequests.length
    expect(upstreamRequestCountBeforeInspect).toBe(0)

    await page.getByLabel('Protocol', { exact: true }).click()
    await page.getByRole('option', { name: 'OpenAI', exact: true }).click()
    await page.getByLabel('Client model').fill(discoveredModel)
    await page.getByLabel('AccessKey', { exact: true }).click()
    await page
      .getByRole('option', { name: new RegExp(`^${accessKeyName} · #\\d+ · Active$`) })
      .click()

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
