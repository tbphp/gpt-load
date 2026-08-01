import type { QueryClient, QueryKey } from '@tanstack/vue-query'

import { controlQueryKeys } from '@/app/query-keys'

export interface MutationInvalidationPlan {
  exact: readonly QueryKey[]
  prefixes: readonly QueryKey[]
}

function plan(
  exact: readonly QueryKey[] = [],
  prefixes: readonly QueryKey[] = [],
): MutationInvalidationPlan {
  return { exact, prefixes }
}

const keyResourcePlan = (groupID: number) =>
  plan(
    [
      controlQueryKeys.groups.keys(groupID),
      controlQueryKeys.groups.detail(groupID),
      controlQueryKeys.health(),
    ],
    [controlQueryKeys.groups.collectionAll, controlQueryKeys.home.all],
  )

export const mutationInvalidationPlans = {
  settings: {
    update: () => plan([], [controlQueryKeys.groups.details()]),
  },
  group: {
    create: plan(
      [controlQueryKeys.groups.options(), controlQueryKeys.health()],
      [controlQueryKeys.groups.collectionAll, controlQueryKeys.home.all],
    ),
    update: (healthAffected: boolean) =>
      plan(
        [controlQueryKeys.groups.options(), ...(healthAffected ? [controlQueryKeys.health()] : [])],
        [controlQueryKeys.groups.collectionAll, controlQueryKeys.home.all],
      ),
    delete: plan(
      [controlQueryKeys.groups.options(), controlQueryKeys.health()],
      [controlQueryKeys.groups.collectionAll, controlQueryKeys.home.all],
    ),
    replaceModels: () =>
      plan(
        [controlQueryKeys.groups.options()],
        [controlQueryKeys.groups.collectionAll, controlQueryKeys.home.all],
      ),
    importKeys: keyResourcePlan,
  },
  upstreamKey: {
    update: keyResourcePlan,
    delete: keyResourcePlan,
  },
  accessKey: {
    create: plan(
      [controlQueryKeys.accessKeys.options(), controlQueryKeys.home.base()],
      [controlQueryKeys.accessKeys.collectionAll],
    ),
    update: plan(
      [controlQueryKeys.accessKeys.options(), controlQueryKeys.home.base()],
      [controlQueryKeys.accessKeys.collectionAll],
    ),
    delete: plan(
      [controlQueryKeys.accessKeys.options(), controlQueryKeys.home.base()],
      [controlQueryKeys.accessKeys.collectionAll],
    ),
    reconcile: plan([controlQueryKeys.accessKeys.options()]),
    reconcileConfirmed: plan([controlQueryKeys.home.base()]),
    reveal: plan(),
  },
  modelPrice: {
    upsert: plan([controlQueryKeys.modelPrices()]),
    reset: plan([controlQueryKeys.modelPrices()]),
  },
} as const

function uniqueQueryKeys(keys: readonly QueryKey[]): QueryKey[] {
  const seen = new Set<string>()
  return keys.filter((queryKey) => {
    const identity = JSON.stringify(queryKey)
    if (seen.has(identity)) return false
    seen.add(identity)
    return true
  })
}

export async function applyInvalidationPlan(
  queryClient: QueryClient,
  invalidationPlan: MutationInvalidationPlan,
  shouldContinue: () => boolean = () => true,
): Promise<void> {
  for (const queryKey of uniqueQueryKeys(invalidationPlan.exact)) {
    if (!shouldContinue()) return
    await queryClient.invalidateQueries({ queryKey, exact: true })
  }
  for (const queryKey of uniqueQueryKeys(invalidationPlan.prefixes)) {
    if (!shouldContinue()) return
    await queryClient.invalidateQueries({ queryKey })
  }
}
