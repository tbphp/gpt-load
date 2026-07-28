import { QueryClient } from '@tanstack/vue-query'

import { controlQueryKeys } from '@/app/query-keys'

import { applyInvalidationPlan, mutationInvalidationPlans } from './invalidation'

describe('resource mutation invalidation graph', () => {
  it('defines exact settings, access-key, and model-price plans', () => {
    expect(mutationInvalidationPlans.settings.update()).toEqual({
      exact: [],
      prefixes: [controlQueryKeys.groups.details()],
    })
    expect(mutationInvalidationPlans.accessKey.create).toEqual({
      exact: [controlQueryKeys.accessKeys.list(), controlQueryKeys.accessKeys.options()],
      prefixes: [],
    })
    expect(mutationInvalidationPlans.accessKey.reconcile).toEqual({
      exact: [controlQueryKeys.accessKeys.options()],
      prefixes: [],
    })
    expect(mutationInvalidationPlans.accessKey.reveal).toEqual({ exact: [], prefixes: [] })
    expect(mutationInvalidationPlans.modelPrice.upsert).toEqual({
      exact: [controlQueryKeys.modelPrices()],
      prefixes: [],
    })
  })

  it('defines group and upstream-key plans without broad control invalidation', () => {
    expect(mutationInvalidationPlans.group.create).toEqual({
      exact: [controlQueryKeys.groups.list(), controlQueryKeys.health()],
      prefixes: [],
    })
    expect(mutationInvalidationPlans.group.update(7, true)).toEqual({
      exact: [controlQueryKeys.groups.list(), controlQueryKeys.health()],
      prefixes: [],
    })
    expect(mutationInvalidationPlans.group.update(7, false)).toEqual({
      exact: [controlQueryKeys.groups.list()],
      prefixes: [],
    })
    expect(mutationInvalidationPlans.group.importKeys(7)).toEqual({
      exact: [
        controlQueryKeys.groups.keys(7),
        controlQueryKeys.groups.detail(7),
        controlQueryKeys.groups.list(),
        controlQueryKeys.health(),
      ],
      prefixes: [],
    })
    expect(mutationInvalidationPlans.upstreamKey.delete(7)).toEqual(
      mutationInvalidationPlans.group.importKeys(7),
    )
    for (const plan of [
      mutationInvalidationPlans.group.create,
      mutationInvalidationPlans.group.update(7, true),
      mutationInvalidationPlans.group.importKeys(7),
    ]) {
      expect([...plan.exact, ...plan.prefixes]).not.toContainEqual(controlQueryKeys.all)
    }
  })

  it('invalidates each exact identity and prefix once', async () => {
    const queryClient = new QueryClient()
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue()
    await applyInvalidationPlan(queryClient, {
      exact: [controlQueryKeys.health(), controlQueryKeys.health()],
      prefixes: [controlQueryKeys.logs.all, controlQueryKeys.logs.all, controlQueryKeys.usage.all],
    })

    expect(invalidate.mock.calls).toEqual([
      [{ queryKey: controlQueryKeys.health(), exact: true }],
      [{ queryKey: controlQueryKeys.logs.all }],
      [{ queryKey: controlQueryKeys.usage.all }],
    ])
  })

  it('stops before starting another invalidation when its owner becomes stale', async () => {
    const queryClient = new QueryClient()
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue()
    let current = true
    invalidate.mockImplementationOnce(async () => {
      current = false
    })
    await applyInvalidationPlan(
      queryClient,
      mutationInvalidationPlans.group.update(7, true),
      () => current,
    )
    expect(invalidate).toHaveBeenCalledTimes(1)
  })
})
