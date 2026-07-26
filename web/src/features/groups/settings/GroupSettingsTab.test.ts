import { QueryClient } from '@tanstack/vue-query'
import { flushPromises } from '@vue/test-utils'

import type { ApiClient, ApiRequestOptions } from '@/api/client'
import type { GroupDetailDto, GroupUpdateResult } from '@/api/control/groups'
import { ApiError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'
import { mountApp } from '@/test/mount-app'

import GroupSettingsTab from './GroupSettingsTab.vue'

const detail: GroupDetailDto = {
  id: 7,
  name: 'Primary',
  upstream_url: 'https://api.example.com/v1',
  protocols: ['openai'],
  models: [{ id: 'gpt-4o', alias: '' }],
  enabled: true,
  key_count: 2,
  validation_model: 'gpt-4o',
  weight_manual: null,
  config: { connect_timeout: 30 },
  effective_config: {
    connect_timeout: 30,
    first_byte_timeout: 120,
    request_timeout: 600,
    stream_idle_timeout: 300,
    header_rules: { set: { 'X-Global': 'HEADER_CANARY_EFFECTIVE' }, remove: ['X-Debug'] },
    inject_usage_options: true,
  },
}

function queryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

async function mountSettings(request: ApiClient['request'], group = detail) {
  const client = queryClient()
  client.setQueryData(controlQueryKeys.groups.detail(group.id), group)
  const mounted = await mountApp(GroupSettingsTab, {
    api: { request },
    queryClient: client,
    path: `/groups/${group.id}?tab=settings`,
    locale: 'en-US',
    mounting: {
      props: { groupId: group.id, group },
      attachTo: document.body,
    },
  })
  return { ...mounted, queryClient: client }
}

function documentButton(selector: string): HTMLButtonElement {
  const element = document.querySelector<HTMLButtonElement>(selector)
  if (!element) throw new Error(`missing ${selector}`)
  return element
}

describe('GroupSettingsTab', () => {
  it.each(['resolve', 'reject'] as const)(
    'ignores a signal-ignoring late %s after unmount without recreating Group detail state',
    async (outcome) => {
      let settle!: (value: GroupUpdateResult) => void
      let fail!: (reason: Error) => void
      const late = new Promise<GroupUpdateResult>((resolve, reject) => {
        settle = resolve
        fail = reject
      })
      const request = vi.fn(() => late) as ApiClient['request']
      const { queryClient, wrapper } = await mountSettings(request)
      const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
      await wrapper.get('[data-test="group-name"]').setValue('Renamed')
      await wrapper.get('[data-test="group-settings-save"]').trigger('click')
      wrapper.unmount()
      queryClient.removeQueries({ queryKey: controlQueryKeys.groups.detail(7), exact: true })

      if (outcome === 'resolve') settle({ group: detail, model_rediscovery_recommended: false })
      else fail(new Error('late ordinary failure'))
      await flushPromises()

      expect(queryClient.getQueryData(controlQueryKeys.groups.detail(7))).toBeUndefined()
      expect(invalidate).not.toHaveBeenCalled()
      expect(document.body.textContent).not.toContain('Unable to update')
    },
  )
  it('does not start health invalidation after unmount during deferred list invalidation', async () => {
    let releaseList!: () => void
    const listInvalidation = new Promise<void>((resolve) => {
      releaseList = resolve
    })
    const request = vi.fn().mockResolvedValue({
      group: { ...detail, name: 'Renamed' },
      model_rediscovery_recommended: false,
    }) as ApiClient['request']
    const { queryClient, wrapper } = await mountSettings(request)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockImplementation((filters) => {
      const resolved = typeof filters === 'function' ? filters() : filters
      if (JSON.stringify(resolved?.queryKey) === JSON.stringify(controlQueryKeys.groups.list()))
        return listInvalidation
      return Promise.resolve()
    })
    await wrapper.get('[data-test="group-name"]').setValue('Renamed')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()
    wrapper.unmount()
    queryClient.removeQueries({ queryKey: controlQueryKeys.groups.detail(7), exact: true })
    releaseList()
    await flushPromises()

    expect(invalidate).toHaveBeenCalledTimes(1)
    expect(queryClient.getQueryData(controlQueryKeys.groups.detail(7))).toBeUndefined()
    expect(document.body.textContent).not.toContain('Unable to update')
  })
  it('disables every mutable Group Settings control while save is pending', async () => {
    const request = vi.fn(() => new Promise(() => {})) as ApiClient['request']
    const { wrapper } = await mountSettings(request)
    await wrapper.get('[data-test="group-name"]').setValue('Renamed')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')

    for (const selector of [
      '[data-test="group-name"]',
      '[data-test="group-upstream-url"]',
      '[data-test="group-validation-model"]',
      '[data-test="group-weight"]',
      '[data-test="group-protocol-openai"]',
      '[data-test="group-enabled"]',
      '[data-test="override-connect_timeout"]',
    ]) {
      expect(wrapper.get(selector).attributes()).toHaveProperty('disabled')
    }
    wrapper.unmount()
  })
  it('renders only Group protocols', async () => {
    const { wrapper } = await mountSettings(vi.fn() as ApiClient['request'])

    expect(wrapper.find('[data-test="group-protocol-openai-response"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('shows inherited/effective values for exactly five runtime fields and no retention setting', async () => {
    const { wrapper } = await mountSettings(vi.fn() as ApiClient['request'])

    expect(wrapper.get('[data-test="runtime-connect_timeout"]').text()).toContain('30')
    expect(wrapper.get('[data-test="runtime-connect_timeout"]').text()).toContain('Override')
    expect(wrapper.get('[data-test="runtime-first_byte_timeout"]').text()).toContain('120')
    expect(wrapper.get('[data-test="runtime-first_byte_timeout"]').text()).toContain('Inherited')
    expect(wrapper.get('[data-test="runtime-request_timeout"]').text()).toContain('600')
    expect(wrapper.get('[data-test="runtime-stream_idle_timeout"]').text()).toContain('300')
    expect(wrapper.get('[data-test="runtime-header_rules"]').text()).toContain('Inherited')
    expect(wrapper.findAll('[data-test^="runtime-"]')).toHaveLength(5)
    expect(wrapper.html()).not.toContain('request_log_retention_days')
    expect(wrapper.html()).not.toContain('HEADER_CANARY_EFFECTIVE')
    wrapper.unmount()
  })

  it('guards normalized no-op submission in both UI and transport', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountSettings(request)

    expect(wrapper.get('[data-test="group-settings-save"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-test="group-name"]').setValue(' Primary ')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')

    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('identifies invalid base and timeout fields beside their controls', async () => {
    const { wrapper } = await mountSettings(vi.fn() as ApiClient['request'])
    await wrapper.get('[data-test="group-name"]').setValue(' ')

    expect(wrapper.get('[data-test="group-name"]').attributes('aria-invalid')).toBe('true')
    expect(wrapper.get('[data-test="group-name-error"]').attributes('role')).toBe('alert')

    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="runtime-request_timeout"] input[type="number"]').setValue('0')

    expect(
      wrapper
        .get('[data-test="runtime-request_timeout"] input[type="number"]')
        .attributes('aria-invalid'),
    ).toBe('true')
    expect(wrapper.get('[data-test="runtime-request_timeout-error"]').attributes('role')).toBe(
      'alert',
    )
    expect(wrapper.get('[data-test="group-settings-save"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('renders an existing manual weight of zero only as a compatibility option', async () => {
    const zeroWeight = { ...detail, weight_manual: 0 }
    const { wrapper } = await mountSettings(vi.fn() as ApiClient['request'], zeroWeight)
    const select = wrapper.get('[data-test="group-weight"]')
    const optionValues = select.findAll('option').map((option) => option.attributes('value'))

    expect((select.element as HTMLSelectElement).value).toBe('0')
    expect(optionValues).toHaveLength(102)
    expect(optionValues[0]).toBe('auto')
    expect(optionValues[1]).toBe('0')
    expect(optionValues.at(-1)).toBe('100')
    expect(wrapper.get('[data-test="group-settings-save"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('does not expose zero as a normal manual Group weight', async () => {
    const { wrapper } = await mountSettings(vi.fn() as ApiClient['request'])
    const optionValues = wrapper
      .get('[data-test="group-weight"]')
      .findAll('option')
      .map((option) => option.attributes('value'))

    expect(optionValues).toHaveLength(101)
    expect(optionValues[0]).toBe('auto')
    expect(optionValues).not.toContain('0')
    expect(optionValues.at(-1)).toBe('100')
    wrapper.unmount()
  })

  it('rebases detail, invalidates list, and invalidates health only for health-embedded base fields', async () => {
    const updated = { ...detail, name: 'Renamed' }
    const result: GroupUpdateResult = { group: updated, model_rediscovery_recommended: false }
    const requestMock = vi.fn(async (path: string, options?: ApiRequestOptions) => {
      if (path === '/api/groups/7' && options?.method === 'PUT') return result
      throw new Error(`unexpected request: ${path}`)
    })
    const { queryClient: client, wrapper } = await mountSettings(
      requestMock as ApiClient['request'],
    )
    const cancel = vi.spyOn(client, 'cancelQueries')
    const setQueryData = vi.spyOn(client, 'setQueryData')
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    await wrapper.get('[data-test="group-name"]').setValue(' Renamed ')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledWith('/api/groups/7', {
      method: 'PUT',
      json: { name: 'Renamed' },
      signal: expect.any(AbortSignal),
    })
    expect(cancel).toHaveBeenCalledWith({
      queryKey: controlQueryKeys.groups.detail(7),
      exact: true,
    })
    expect(cancel.mock.invocationCallOrder[0]).toBeLessThan(
      setQueryData.mock.invocationCallOrder[0]!,
    )
    expect(setQueryData).toHaveBeenCalledWith(controlQueryKeys.groups.detail(7), updated)
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.list() },
      { queryKey: controlQueryKeys.health() },
    ])
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    wrapper.unmount()
  })

  it('does not invalidate health for validation-model-only updates', async () => {
    const updated = { ...detail, validation_model: null }
    const request = vi.fn().mockResolvedValue({
      group: updated,
      model_rediscovery_recommended: false,
    }) as ApiClient['request']
    const { queryClient: client, wrapper } = await mountSettings(request)
    const invalidate = vi.spyOn(client, 'invalidateQueries')

    await wrapper.get('[data-test="group-validation-model"]').setValue('')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/groups/7', {
      method: 'PUT',
      json: { validation_model: null },
      signal: expect.any(AbortSignal),
    })
    expect(invalidate.mock.calls.map(([filters]) => filters)).toEqual([
      { queryKey: controlQueryKeys.groups.list() },
    ])
    wrapper.unmount()
  })

  it('sends an ordinary URL update first and only confirms the exact confirmation-required branch', async () => {
    const changed = { ...detail, upstream_url: 'https://new.example.com/v1' }
    const requestMock = vi
      .fn()
      .mockRejectedValueOnce(
        new ApiError(409, 'UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED', 'localized'),
      )
      .mockResolvedValueOnce({ group: changed, model_rediscovery_recommended: true })
    const { wrapper } = await mountSettings(requestMock as ApiClient['request'])
    const save = wrapper.get('[data-test="group-settings-save"]').element as HTMLButtonElement
    await wrapper.get('[data-test="group-upstream-url"]').setValue('https://new.example.com/v1')
    save.focus()
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenNthCalledWith(1, '/api/groups/7', {
      method: 'PUT',
      json: { upstream_url: 'https://new.example.com/v1' },
      signal: expect.any(AbortSignal),
    })
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    const heading = wrapper.get('#group-settings-heading').element as HTMLHeadingElement
    const headingFocus = vi.spyOn(heading, 'focus')
    documentButton('[data-test="group-url-confirm"]').click()
    await flushPromises()
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(save.disabled).toBe(true)
    expect(document.activeElement).toBe(heading)
    expect(headingFocus).toHaveBeenCalledTimes(1)

    expect(requestMock).toHaveBeenNthCalledWith(2, '/api/groups/7', {
      method: 'PUT',
      json: {
        upstream_url: 'https://new.example.com/v1',
        confirm_upstream_url_change: true,
      },
      signal: expect.any(AbortSignal),
    })
    expect(wrapper.get('[data-test="group-rediscovery-recommended"]').text()).toContain(
      'rediscover',
    )
    expect(wrapper.get('[data-test="group-rediscovery-action"]').attributes('href')).toBe(
      '/groups/7?tab=models',
    )
    wrapper.unmount()
  })

  it('restores real Save focus after URL confirmation closes', async () => {
    const request = vi
      .fn()
      .mockRejectedValueOnce(
        new ApiError(409, 'UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED', 'confirmation required'),
      ) as ApiClient['request']
    const { wrapper } = await mountSettings(request)
    const save = wrapper.get('[data-test="group-settings-save"]').element as HTMLButtonElement
    await wrapper.get('[data-test="group-upstream-url"]').setValue('https://new.example.com/v1')
    save.focus()
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    const saveFocus = vi.spyOn(save, 'focus')
    document.querySelector<HTMLButtonElement>('.app-dialog__close')?.click()
    await flushPromises()
    expect(document.activeElement).toBe(save)
    expect(save.disabled).toBe(false)
    expect(saveFocus).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('restores Save focus after URL confirmation Cancel', async () => {
    const request = vi
      .fn()
      .mockRejectedValueOnce(
        new ApiError(409, 'UPSTREAM_URL_CHANGE_CONFIRMATION_REQUIRED', 'confirmation required'),
      ) as ApiClient['request']
    const { wrapper } = await mountSettings(request)
    const save = wrapper.get('[data-test="group-settings-save"]').element as HTMLButtonElement
    await wrapper.get('[data-test="group-upstream-url"]').setValue('https://new.example.com/v1')
    save.focus()
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()
    const saveFocus = vi.spyOn(save, 'focus')
    document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')[1]?.click()
    await flushPromises()

    expect(document.activeElement).toBe(save)
    expect(save.disabled).toBe(false)
    expect(saveFocus).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('keeps the rediscovery recommendation after a later unrelated successful save returns false', async () => {
    const changedURL = { ...detail, upstream_url: 'https://new.example.com/v1' }
    const renamed = { ...changedURL, name: 'Renamed' }
    const request = vi
      .fn()
      .mockResolvedValueOnce({ group: changedURL, model_rediscovery_recommended: true })
      .mockResolvedValueOnce({ group: renamed, model_rediscovery_recommended: false })
    const { wrapper } = await mountSettings(request as ApiClient['request'])

    await wrapper.get('[data-test="group-upstream-url"]').setValue(changedURL.upstream_url)
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="group-rediscovery-recommended"]').exists()).toBe(true)

    await wrapper.get('[data-test="group-name"]').setValue('Renamed')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="group-rediscovery-recommended"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps the rediscovery recommendation when a later unrelated save fails', async () => {
    const changedURL = { ...detail, upstream_url: 'https://new.example.com/v1' }
    const request = vi
      .fn()
      .mockResolvedValueOnce({ group: changedURL, model_rediscovery_recommended: true })
      .mockRejectedValueOnce(new Error('later failure'))
    const { wrapper } = await mountSettings(request as ApiClient['request'])

    await wrapper.get('[data-test="group-upstream-url"]').setValue(changedURL.upstream_url)
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="group-rediscovery-recommended"]').exists()).toBe(true)

    await wrapper.get('[data-test="group-name"]').setValue('Renamed')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="group-rediscovery-recommended"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Unable to update')
    wrapper.unmount()
  })

  it('blocks UPSTREAM_URL_CONFLICT without ever adding the confirmation flag', async () => {
    const requestMock = vi.fn().mockRejectedValue(
      new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'localized conflict', {
        groups: [{ id: 9, name: 'Existing' }],
      }),
    )
    const { wrapper } = await mountSettings(requestMock as ApiClient['request'])

    await wrapper.get('[data-test="group-upstream-url"]').setValue('https://used.example.com/v1')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(requestMock).toHaveBeenCalledTimes(1)
    expect(requestMock).toHaveBeenCalledWith('/api/groups/7', {
      method: 'PUT',
      json: { upstream_url: 'https://used.example.com/v1' },
      signal: expect.any(AbortSignal),
    })
    expect(wrapper.get('[data-test="group-url-conflict"]').text()).toContain('Existing')
    expect(document.querySelector('[data-test="group-url-confirm"]')).toBeNull()
    expect(JSON.stringify(requestMock.mock.calls)).not.toContain('confirm_upstream_url_change')
    wrapper.unmount()
  })

  it('falls back to fixed generic feedback for malformed conflict data', async () => {
    const request = vi
      .fn()
      .mockRejectedValue(
        new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'must not render', { groups: [] }),
      ) as ApiClient['request']
    const { wrapper } = await mountSettings(request)
    await wrapper.get('[data-test="group-upstream-url"]').setValue('https://used.example.com/v1')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Unable to update')
    expect(wrapper.find('[data-test="group-url-conflict"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([
    { groups: [{ id: Number.MAX_SAFE_INTEGER + 1, name: 'Unsafe' }] },
    { groups: [{ id: 9, name: '   ' }] },
  ])('falls back to generic save feedback for unsafe or blank conflict entries', async (data) => {
    const request = vi
      .fn()
      .mockRejectedValue(
        new ApiError(409, 'UPSTREAM_URL_CONFLICT', 'must not render', data),
      ) as ApiClient['request']
    const { wrapper } = await mountSettings(request)
    await wrapper.get('[data-test="group-upstream-url"]').setValue('https://used.example.com/v1')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Unable to update')
    expect(wrapper.find('[data-test="group-url-conflict"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('fully rebases a clean draft when refreshed Group props arrive', async () => {
    const { wrapper } = await mountSettings(vi.fn() as ApiClient['request'])
    const refreshed = {
      ...detail,
      config: {},
      effective_config: { ...detail.effective_config, connect_timeout: 15, request_timeout: 900 },
    }

    await wrapper.setProps({ group: refreshed })
    await flushPromises()

    expect(wrapper.get('[data-test="runtime-connect_timeout"]').text()).toContain('15')
    expect(wrapper.get('[data-test="runtime-request_timeout"]').text()).toContain('900')
    expect(wrapper.get('[data-test="group-settings-save"]').attributes()).toHaveProperty('disabled')
    wrapper.unmount()
  })

  it('does not turn externally refreshed untouched fields into a local patch', async () => {
    const updated = { ...detail, name: 'Local name', enabled: false }
    const request = vi.fn().mockResolvedValue({
      group: updated,
      model_rediscovery_recommended: false,
    }) as ApiClient['request']
    const { wrapper } = await mountSettings(request)
    await wrapper.get('[data-test="group-name"]').setValue('Local name')
    await wrapper.setProps({ group: { ...detail, enabled: false } })
    await flushPromises()

    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/groups/7', {
      method: 'PUT',
      json: { name: 'Local name' },
      signal: expect.any(AbortSignal),
    })
    wrapper.unmount()
  })

  it('preserves a dirty draft while using refreshed effective values for newly enabled overrides', async () => {
    const { wrapper } = await mountSettings(vi.fn() as ApiClient['request'])
    await wrapper.get('[data-test="group-name"]').setValue('Local name')
    await wrapper.setProps({
      group: { ...detail, effective_config: { ...detail.effective_config, request_timeout: 900 } },
    })
    await flushPromises()
    expect((wrapper.get('[data-test="group-name"]').element as HTMLInputElement).value).toBe(
      'Local name',
    )
    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)

    expect(
      (
        wrapper.get('[data-test="runtime-request_timeout"] input[type="number"]')
          .element as HTMLInputElement
      ).value,
    ).toBe('900')
    wrapper.unmount()
  })

  it('seeds and warns HeaderRules replacement, removes overrides, and saves complete sparse config', async () => {
    const updated: GroupDetailDto = {
      ...detail,
      config: { connect_timeout: 30, request_timeout: 600 },
    }
    const request = vi.fn().mockResolvedValue({
      group: updated,
      model_rediscovery_recommended: false,
    }) as ApiClient['request']
    const { wrapper } = await mountSettings(request)

    await wrapper.get('[data-test="override-header_rules"]').setValue(true)
    expect(wrapper.get('[data-test="header-rules-replacement-warning"]').text()).toContain(
      'replaces',
    )
    expect(wrapper.get('[data-test="header-value"]').attributes('type')).toBe('password')
    expect(wrapper.text()).not.toContain('HEADER_CANARY_EFFECTIVE')

    await wrapper.get('[data-test="override-header_rules"]').setValue(false)
    expect(wrapper.find('[data-test="header-rules-replacement-warning"]').exists()).toBe(false)
    await wrapper.get('[data-test="override-request_timeout"]').setValue(true)
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    await flushPromises()

    expect(request).toHaveBeenCalledWith('/api/groups/7', {
      method: 'PUT',
      json: { config: { connect_timeout: 30, request_timeout: 600 } },
      signal: expect.any(AbortSignal),
    })
    wrapper.unmount()
  })

  it('blocks saving when HeaderRules contain an exact duplicate name', async () => {
    const request = vi.fn() as ApiClient['request']
    const { wrapper } = await mountSettings(request)

    await wrapper.get('[data-test="override-header_rules"]').setValue(true)
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.findAll('[data-test="header-name"]').at(-1)!.setValue('X-Duplicate')
    await wrapper.get('[data-test="add-header-rule"]').trigger('click')
    await wrapper.findAll('[data-test="header-name"]').at(-1)!.setValue('X-Duplicate')

    expect(wrapper.get('[data-test="group-settings-save"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-test="group-settings-save"]').trigger('click')
    expect(request).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
