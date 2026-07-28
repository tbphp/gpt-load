import { useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { useApiClient } from '@/api/client-context'
import {
  getSettings,
  projectSettingsConflict,
  updateSettings,
  type RuntimeSettingKey,
  type SettingsPatch,
} from '@/app/resources/settings'
import { RequestCancelledError } from '@/api/errors'
import { classifyMutationOutcome } from '@/app/mutation-outcome'
import { controlQueryKeys } from '@/app/query-keys'
import type { SettingsResource } from '@/app/resources/settings'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'

import {
  buildSettingsPatch,
  createSettingsDraft,
  replaceDraftFieldFromSettings,
  validateSettingsSection,
  type SettingsDraft,
} from './settings-patch'
import {
  chooseSettingsMutationResult,
  mergeSettingsConflict,
  reconcileSettingsMutation,
  type SettingsMergeConflict,
} from './settings-response'

export interface SettingsDraftChange {
  key: RuntimeSettingKey
  draft: SettingsDraft
}

export interface SettingsPageController {
  base: Readonly<Ref<SettingsResource | null>>
  draft: Readonly<Ref<SettingsDraft | null>>
  patch: ComputedRef<SettingsPatch>
  dirty: ComputedRef<boolean>
  valid: ComputedRef<boolean>
  pending: Readonly<Ref<boolean>>
  failed: Readonly<Ref<boolean>>
  indeterminate: Readonly<Ref<boolean>>
  reconciling: Readonly<Ref<boolean>>
  concurrent: Readonly<Ref<boolean>>
  operationLocked: ComputedRef<boolean>
  conflicts: Readonly<Ref<SettingsMergeConflict[]>>
  savedAt: Readonly<Ref<Date | null>>
  updateDraft(change: SettingsDraftChange): void
  chooseMine(key: RuntimeSettingKey): void
  chooseLatest(key: RuntimeSettingKey): void
  discard(): void
  saveAll(): Promise<void>
  checkResult(): Promise<void>
}

export interface SettingsControllerOptions {
  now?: () => Date
}

interface UnknownSettingsOperation {
  base: SettingsResource
  draft: SettingsDraft
}

function cloneDraft(draft: SettingsDraft): SettingsDraft {
  return createSettingsDraft({
    values: draft.values,
    overrides: [...draft.overrides],
  })
}

export function useSettingsController(
  resource: Readonly<Ref<SettingsResource | null>>,
  options: SettingsControllerOptions = {},
): SettingsPageController {
  const client = useApiClient()
  const queryClient = useQueryClient()
  const { locale } = useI18n()
  const now = options.now ?? (() => new Date())
  const base = ref<SettingsResource | null>(resource.value)
  const draft = ref<SettingsDraft | null>(
    resource.value ? createSettingsDraft(resource.value.settings) : null,
  )
  const pending = ref(false)
  const failed = ref(false)
  const indeterminate = ref(false)
  const reconciling = ref(false)
  const concurrent = ref(false)
  const conflicts = ref<SettingsMergeConflict[]>([])
  const savedAt = ref<Date | null>(null)
  let requestOwner = 0
  let requestController: AbortController | undefined
  let unknownOperation: UnknownSettingsOperation | undefined
  let mounted = true

  const settingsQueryKey = () => controlQueryKeys.settings(locale.value)
  const patch = computed<SettingsPatch>(() => {
    if (!base.value || !draft.value) return {}
    return buildSettingsPatch(base.value.settings, draft.value, 'all')
  })
  const dirty = computed(() => Object.keys(patch.value).length > 0)
  const valid = computed(
    () =>
      draft.value !== null &&
      conflicts.value.length === 0 &&
      validateSettingsSection(draft.value, 'request-forwarding') &&
      validateSettingsSection(draft.value, 'logs-maintenance'),
  )
  const operationLocked = computed(() => pending.value || indeterminate.value || reconciling.value)

  function isCurrent(owner: number, controller: AbortController): boolean {
    return (
      mounted &&
      owner === requestOwner &&
      requestController === controller &&
      !controller.signal.aborted
    )
  }

  function clearConflict(key: RuntimeSettingKey): void {
    conflicts.value = conflicts.value.filter((conflict) => conflict.key !== key)
    if (conflicts.value.length === 0) concurrent.value = false
  }

  function rebase(next: SettingsResource): void {
    base.value = next
    draft.value = createSettingsDraft(next.settings)
    failed.value = false
    indeterminate.value = false
    reconciling.value = false
    concurrent.value = false
    conflicts.value = []
    unknownOperation = undefined
  }

  async function markConfirmed(next: SettingsResource, nextDraft: SettingsDraft): Promise<void> {
    base.value = next
    draft.value = nextDraft
    failed.value = false
    indeterminate.value = false
    concurrent.value = false
    conflicts.value = []
    unknownOperation = undefined
    savedAt.value = new Date(now().getTime())
    queryClient.setQueryData(settingsQueryKey(), next)
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.settings.update())
  }

  async function applyUnknownLatest(latest: SettingsResource): Promise<void> {
    const operation = unknownOperation
    if (!operation) return
    const result = reconcileSettingsMutation(operation.base, operation.draft, latest, 'all')
    base.value = result.resource
    draft.value = result.draft
    conflicts.value = result.conflicts
    queryClient.setQueryData(settingsQueryKey(), latest)

    if (result.kind === 'confirmed') {
      await markConfirmed(result.resource, result.draft)
      return
    }
    if (result.kind === 'conflict') {
      unknownOperation = undefined
      indeterminate.value = false
      concurrent.value = true
      return
    }
    indeterminate.value = true
  }

  function acceptExternalSettings(next: SettingsResource | null): void {
    if (!next) return
    if (!base.value || !draft.value) {
      rebase(next)
      return
    }
    if (next.settings_etag === base.value.settings_etag) return
    if (unknownOperation) {
      void applyUnknownLatest(next)
      return
    }
    if (dirty.value) {
      const merged = mergeSettingsConflict(base.value, draft.value, next, 'all')
      base.value = merged.resource
      draft.value = merged.draft
      conflicts.value = merged.conflicts
      concurrent.value = true
      return
    }
    rebase(next)
  }

  watch(resource, acceptExternalSettings)

  function updateDraft(change: SettingsDraftChange): void {
    if (operationLocked.value || !draft.value) return
    draft.value = cloneDraft(change.draft)
    failed.value = false
    clearConflict(change.key)
  }

  function chooseMine(key: RuntimeSettingKey): void {
    if (operationLocked.value) return
    clearConflict(key)
  }

  function chooseLatest(key: RuntimeSettingKey): void {
    if (operationLocked.value || !base.value || !draft.value) return
    draft.value = replaceDraftFieldFromSettings(draft.value, base.value.settings, key)
    clearConflict(key)
  }

  function discard(): void {
    if (operationLocked.value || !base.value) return
    draft.value = createSettingsDraft(base.value.settings)
    failed.value = false
    concurrent.value = false
    conflicts.value = []
  }

  async function reconcileUnknownOperation(
    owner: number,
    controller: AbortController,
  ): Promise<void> {
    if (!unknownOperation || !isCurrent(owner, controller)) return
    reconciling.value = true
    try {
      const latest = await getSettings(client, controller.signal)
      if (!isCurrent(owner, controller)) return
      await applyUnknownLatest(latest)
    } catch (error: unknown) {
      if (!isCurrent(owner, controller) || error instanceof RequestCancelledError) {
        return
      }
      indeterminate.value = true
    } finally {
      if (isCurrent(owner, controller)) reconciling.value = false
    }
  }

  async function checkResult(): Promise<void> {
    if (!unknownOperation || reconciling.value || pending.value) return
    requestController?.abort()
    const owner = ++requestOwner
    const controller = new AbortController()
    requestController = controller
    await reconcileUnknownOperation(owner, controller)
    if (isCurrent(owner, controller)) requestController = undefined
  }

  async function saveAll(): Promise<void> {
    if (
      operationLocked.value ||
      !valid.value ||
      !base.value ||
      !draft.value ||
      Object.keys(patch.value).length === 0
    ) {
      return
    }

    const operationBase = base.value
    const operationDraft = cloneDraft(draft.value)
    const normalizedPatch = buildSettingsPatch(operationBase.settings, operationDraft, 'all')
    requestController?.abort()
    const owner = ++requestOwner
    const controller = new AbortController()
    requestController = controller
    pending.value = true
    failed.value = false
    indeterminate.value = false
    concurrent.value = false

    try {
      const response = await updateSettings(
        client,
        normalizedPatch,
        operationBase.settings_etag,
        controller.signal,
      )
      if (!isCurrent(owner, controller)) return
      await queryClient.cancelQueries({ queryKey: settingsQueryKey(), exact: true })
      if (!isCurrent(owner, controller)) return

      const cached = queryClient.getQueryData<SettingsResource>(settingsQueryKey())
      const decision = chooseSettingsMutationResult(
        response,
        cached,
        operationBase,
        operationDraft,
        'all',
      )
      if (decision.kind === 'refetch') {
        await queryClient.refetchQueries({
          queryKey: settingsQueryKey(),
          exact: true,
        })
        if (!isCurrent(owner, controller)) return
        const refreshed = queryClient.getQueryData<SettingsResource>(settingsQueryKey())
        const refreshedDecision = chooseSettingsMutationResult(
          response,
          refreshed,
          operationBase,
          operationDraft,
          'all',
        )
        if (refreshedDecision.kind === 'apply') {
          await markConfirmed(refreshedDecision.resource, refreshedDecision.draft)
          return
        }
        if (refreshed && refreshed.settings_etag !== cached?.settings_etag) {
          acceptExternalSettings(refreshed)
        }
        return
      }

      await markConfirmed(decision.resource, decision.draft)
    } catch (error: unknown) {
      if (!isCurrent(owner, controller) || error instanceof RequestCancelledError) return
      const latest = projectSettingsConflict(error)
      if (latest) {
        const merged = mergeSettingsConflict(operationBase, operationDraft, latest, 'all')
        base.value = merged.resource
        draft.value = merged.draft
        conflicts.value = merged.conflicts
        concurrent.value = true
        queryClient.setQueryData(settingsQueryKey(), latest)
        return
      }

      const outcome = classifyMutationOutcome<SettingsResource>({
        kind: 'error',
        error,
        requestSent: true,
      })
      if (outcome.kind === 'failed') {
        failed.value = true
        return
      }
      unknownOperation = { base: operationBase, draft: operationDraft }
      indeterminate.value = true
      await reconcileUnknownOperation(owner, controller)
    } finally {
      if (isCurrent(owner, controller)) {
        requestController = undefined
        pending.value = false
      }
    }
  }

  onBeforeUnmount(() => {
    mounted = false
    requestOwner += 1
    requestController?.abort()
    requestController = undefined
    unknownOperation = undefined
  })

  return {
    base,
    draft,
    patch,
    dirty,
    valid,
    pending,
    failed,
    indeterminate,
    reconciling,
    concurrent,
    operationLocked,
    conflicts,
    savedAt,
    updateDraft,
    chooseMine,
    chooseLatest,
    discard,
    saveAll,
    checkResult,
  }
}
