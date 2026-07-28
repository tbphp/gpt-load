import { toValue, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'

import type { ApiClient } from '@/api/client'
import { controlQueryKeys } from '@/app/query-keys'

import { accessKeyListQueryOptions, accessKeyOptionsQueryOptions } from './access-keys'
import { groupDetailQueryOptions, groupListQueryOptions } from './groups'
import { healthQueryOptions } from './health'
import { modelPriceQueryOptions } from './model-prices'
import { requestLogInfiniteQueryOptions } from './request-logs'
import { settingsQueryOptions } from './settings'
import { systemInfoQueryOptions } from './system-info'
import { upstreamKeyListQueryOptions } from './upstream-keys'
import { usageQueryOptions } from './usage'

const client: ApiClient = {
  request: vi.fn(),
}

describe('resource query options', () => {
  it('owns static resource identities and ephemeral cache policies', () => {
    expect(groupListQueryOptions(client).queryKey).toEqual(controlQueryKeys.groups.list())
    expect(healthQueryOptions(client).queryKey).toEqual(controlQueryKeys.health())
    expect(modelPriceQueryOptions(client).queryKey).toEqual(controlQueryKeys.modelPrices())
    expect(systemInfoQueryOptions(client).queryKey).toEqual(controlQueryKeys.systemInfo())

    const accessKeys = accessKeyListQueryOptions(client)
    const accessKeyOptions = accessKeyOptionsQueryOptions(client)
    expect(accessKeys.queryKey).toEqual(controlQueryKeys.accessKeys.list())
    expect(accessKeys.gcTime).toBe(0)
    expect(accessKeyOptions.queryKey).toEqual(controlQueryKeys.accessKeys.options())
    expect(accessKeyOptions.gcTime).toBe(0)
  })

  it('derives reactive detail, key, and locale identities inside resources', () => {
    const groupID = ref<number>()
    const locale = ref('zh-CN')

    const detail = groupDetailQueryOptions(client, groupID)
    const keys = upstreamKeyListQueryOptions(client, () => groupID.value ?? 0)
    const settings = settingsQueryOptions(client, locale)

    expect(toValue(detail.queryKey)).toEqual(controlQueryKeys.groups.details())
    expect(toValue(detail.enabled)).toBe(false)
    groupID.value = 7
    expect(toValue(detail.queryKey)).toEqual(controlQueryKeys.groups.detail(7))
    expect(toValue(detail.enabled)).toBe(true)
    expect(toValue(keys.queryKey)).toEqual(controlQueryKeys.groups.keys(7))

    expect(toValue(settings.queryKey)).toEqual(controlQueryKeys.settings('zh-CN'))
    locale.value = 'en-US'
    expect(toValue(settings.queryKey)).toEqual(controlQueryKeys.settings('en-US'))
    expect(settings.gcTime).toBe(0)
  })

  it('owns polling and infinite-query transport policies', () => {
    const health = healthQueryOptions(client, true)
    expect(health.refetchInterval).toBe(10_000)
    expect(health.refetchIntervalInBackground).toBe(false)
    expect(health.refetchOnWindowFocus).toBe(false)

    const filters = ref({ range: '24h' as const })
    const usage = usageQueryOptions(client, filters)
    expect(toValue(usage.queryKey)).toEqual(controlQueryKeys.usage.report({ range: '24h' }))

    const logs = requestLogInfiniteQueryOptions(
      client,
      ref({ status: 'success' as const, selected_request_id: 'presentation-only' }),
    )
    expect(toValue(logs.queryKey)).toEqual(controlQueryKeys.logs.list({ status: 'success' }))
    expect(logs.initialPageParam).toBeNull()
    expect(logs.gcTime).toBe(0)
  })
})
