import type { ApiClient } from '@/api/client'
import { InvalidResponseError } from '@/api/errors'
import { controlQueryKeys } from '@/app/query-keys'

import {
  accessKeyResources,
  accessKeyMutationInvalidations,
  listAccessKeys,
  projectAccessKeyMetadata,
  revealAccessKey,
} from './access-keys'

const metadata = {
  id: 9,
  name: 'Client',
  masked_key: 'sk-gl-••••••••cafe',
  status: 'active',
  filters: { groups: [7], protocols: ['openai'], models: ['gpt-5.6'] },
  rpm_limit: 0,
  created_at: '2026-07-29T01:00:00Z',
  updated_at: '2026-07-29T02:00:00Z',
} as const

describe('AccessKey resource policy', () => {
  it('allows only metadata in list/options caches and owns session cleanup', () => {
    expect(accessKeyResources.list).toMatchObject({
      queryKey: controlQueryKeys.accessKeys.list(),
      gcTime: 0,
      cleanup: 'authenticated-session',
      optimisticUpdates: false,
    })
    expect(accessKeyResources.options).toMatchObject({
      queryKey: controlQueryKeys.accessKeys.options(),
      gcTime: 0,
      cleanup: 'authenticated-session',
      optimisticUpdates: false,
    })
    expect(accessKeyResources.list.allowedFields).toEqual([
      'id',
      'name',
      'masked_key',
      'status',
      'filters',
      'rpm_limit',
      'created_at',
      'updated_at',
    ])
    expect(accessKeyResources.options.allowedFields).toEqual(['id', 'name', 'status'])
    expect(JSON.stringify(accessKeyResources)).not.toContain('"key"')
  })

  it('centralizes the mutation invalidation graph', () => {
    expect(accessKeyMutationInvalidations).toEqual({
      create: [controlQueryKeys.accessKeys.list(), controlQueryKeys.accessKeys.options()],
      update: [controlQueryKeys.accessKeys.list(), controlQueryKeys.accessKeys.options()],
      delete: [controlQueryKeys.accessKeys.list(), controlQueryKeys.accessKeys.options()],
      reveal: [],
    })
  })

  it('fails closed on unknown scope values before metadata reaches cache', async () => {
    for (const unsafe of [
      { ...metadata, filters: { ...metadata.filters, groups: [0] } },
      { ...metadata, filters: { ...metadata.filters, protocols: ['future-protocol'] } },
      { ...metadata, filters: { ...metadata.filters, models: [''] } },
      { ...metadata, filters: { ...metadata.filters, plaintext_key: 'CANARY' } },
    ]) {
      expect(() => projectAccessKeyMetadata(unsafe)).toThrow(InvalidResponseError)
    }

    const request = vi.fn().mockResolvedValue([metadata]) as ApiClient['request']
    await expect(listAccessKeys({ request })).resolves.toEqual([metadata])
  })

  it('keeps reveal plaintext in a one-shot result outside metadata resources', async () => {
    const request = vi.fn().mockResolvedValue({
      id: 9,
      key: 'sk-gl-REVEAL-CANARY',
      revealed_at: '2026-07-29T02:00:00Z',
    }) as ApiClient['request']

    await expect(revealAccessKey({ request }, 9)).resolves.toEqual({
      id: 9,
      key: 'sk-gl-REVEAL-CANARY',
      revealed_at: '2026-07-29T02:00:00Z',
    })
    expect(JSON.stringify(accessKeyResources)).not.toContain('REVEAL-CANARY')
    expect(accessKeyMutationInvalidations.reveal).toEqual([])
  })
})
