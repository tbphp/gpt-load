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
  plan([
    controlQueryKeys.groups.keys(groupID),
    controlQueryKeys.groups.detail(groupID),
    controlQueryKeys.groups.list(),
    controlQueryKeys.health(),
    controlQueryKeys.home.base(),
  ])

export const mutationInvalidationPlans = {
  settings: {
    update: () => plan([], [controlQueryKeys.groups.details()]),
  },
  group: {
    create: plan([
      controlQueryKeys.groups.list(),
      controlQueryKeys.health(),
      controlQueryKeys.home.base(),
    ]),
    update: (healthAffected: boolean) =>
      plan([
        controlQueryKeys.groups.list(),
        ...(healthAffected ? [controlQueryKeys.health()] : []),
        controlQueryKeys.home.base(),
      ]),
    delete: plan([
      controlQueryKeys.groups.list(),
      controlQueryKeys.health(),
      controlQueryKeys.home.base(),
    ]),
    replaceModels: () => plan([controlQueryKeys.groups.list(), controlQueryKeys.home.base()]),
    importKeys: keyResourcePlan,
  },
  upstreamKey: {
    update: keyResourcePlan,
    delete: keyResourcePlan,
  },
  accessKey: {
    create: plan([
      controlQueryKeys.accessKeys.list(),
      controlQueryKeys.accessKeys.options(),
      controlQueryKeys.home.base(),
    ]),
    update: plan([
      controlQueryKeys.accessKeys.list(),
      controlQueryKeys.accessKeys.options(),
      controlQueryKeys.home.base(),
    ]),
    delete: plan([
      controlQueryKeys.accessKeys.list(),
      controlQueryKeys.accessKeys.options(),
      controlQueryKeys.home.base(),
    ]),
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
