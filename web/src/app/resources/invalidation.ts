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

const importedKeyResourcePlan = (groupID: number) =>
  plan(
    [controlQueryKeys.groups.summary(groupID), controlQueryKeys.health()],
    [
      controlQueryKeys.groups.keysAll(groupID),
      controlQueryKeys.groups.collectionAll,
      controlQueryKeys.home.all,
    ],
  )

export const mutationInvalidationPlans = {
  settings: {
    update: () => plan([], [controlQueryKeys.groups.settingsAll()]),
  },
  group: {
    create: plan(
      [controlQueryKeys.groups.options(), controlQueryKeys.health()],
      [
        controlQueryKeys.groups.collectionAll,
        controlQueryKeys.home.all,
        controlQueryKeys.modelPrices(),
        controlQueryKeys.providers.modelsAll(),
      ],
    ),
    delete: plan(
      [controlQueryKeys.groups.options(), controlQueryKeys.health()],
      [
        controlQueryKeys.groups.collectionAll,
        controlQueryKeys.home.all,
        controlQueryKeys.modelPrices(),
        controlQueryKeys.providers.modelsAll(),
      ],
    ),
    importKeys: importedKeyResourcePlan,
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
    reconcile: plan(
      [controlQueryKeys.accessKeys.options()],
      [controlQueryKeys.accessKeys.collectionAll],
    ),
    reconcileConfirmed: plan(
      [controlQueryKeys.home.base()],
      [controlQueryKeys.accessKeys.collectionAll],
    ),
    reveal: plan(),
  },
  modelPrice: {
    update: plan(
      [],
      [
        controlQueryKeys.modelPrices(),
        controlQueryKeys.providers.modelsAll(),
        controlQueryKeys.groups.modelsAll(),
      ],
    ),
    reset: plan(
      [],
      [
        controlQueryKeys.modelPrices(),
        controlQueryKeys.providers.modelsAll(),
        controlQueryKeys.groups.modelsAll(),
      ],
    ),
    delete: plan(
      [],
      [
        controlQueryKeys.modelPrices(),
        controlQueryKeys.providers.modelsAll(),
        controlQueryKeys.groups.modelsAll(),
      ],
    ),
    sync: plan(
      [],
      [
        controlQueryKeys.modelPrices(),
        controlQueryKeys.providers.modelsAll(),
        controlQueryKeys.groups.modelsAll(),
      ],
    ),
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
