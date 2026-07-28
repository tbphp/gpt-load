import { controlQueryKeys } from '@/app/query-keys'

import { accessKeyResources, accessKeyMutationInvalidations } from './access-keys'

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
})
