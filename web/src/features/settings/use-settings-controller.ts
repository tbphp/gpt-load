import { useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { controlQueryKeys } from '@/app/query-keys'
import { applyInvalidationPlan, mutationInvalidationPlans } from '@/app/resources/invalidation'
import type { SettingsResource } from '@/app/resources/settings'
import {
  updateSettings,
  type RuntimeSettingKey,
  type SettingsPatch,
} from '@/app/resources/settings'

import {
  buildSettingsPatch,
  createSettingsDraft,
  validateSettingsSection,
  type SettingsDraft,
} from './settings-patch'

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
  operationLocked: ComputedRef<boolean>
  savedAt: Readonly<Ref<Date | null>>
  updateDraft(change: SettingsDraftChange): void
  discard(): void
  saveAll(extra?: SettingsPatch): Promise<void>
}

export interface SettingsControllerOptions {
  now?: () => Date
  hasLocalEdits?: Readonly<Ref<boolean>>
}

function cloneDraft(draft: SettingsDraft): SettingsDraft {
  return createSettingsDraft({
    values: draft.values,
    overrides: [...draft.overrides],
    read_only: [...draft.readOnly],
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
  const savedAt = ref<Date | null>(null)
  let requestOwner = 0
  let requestController: AbortController | undefined
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
      validateSettingsSection(draft.value, 'request-forwarding') &&
      validateSettingsSection(draft.value, 'affinity') &&
      validateSettingsSection(draft.value, 'browser-access') &&
      validateSettingsSection(draft.value, 'logs-maintenance') &&
      validateSettingsSection(draft.value, 'model-prices'),
  )
  const operationLocked = computed(() => pending.value)

  function isCurrent(owner: number, controller: AbortController): boolean {
    return (
      mounted &&
      owner === requestOwner &&
      requestController === controller &&
      !controller.signal.aborted
    )
  }

  function reset(next: SettingsResource): void {
    base.value = next
    draft.value = createSettingsDraft(next.settings)
    failed.value = false
  }

  function consumeCurrentResource(): void {
    const next = resource.value
    if (!next || next === base.value) {
      return
    }
    if (!base.value || !draft.value) {
      reset(next)
      return
    }
    if (dirty.value || options.hasLocalEdits?.value || pending.value) {
      return
    }
    reset(next)
  }

  watch(resource, consumeCurrentResource)
  watch(dirty, (isDirty) => {
    if (!isDirty) consumeCurrentResource()
  })
  if (options.hasLocalEdits) {
    watch(options.hasLocalEdits, (hasLocalEdits) => {
      if (!hasLocalEdits) consumeCurrentResource()
    })
  }

  function updateDraft(change: SettingsDraftChange): void {
    if (operationLocked.value || !draft.value) return
    draft.value = cloneDraft(change.draft)
    failed.value = false
  }

  function discard(): void {
    if (operationLocked.value || !base.value) return
    reset(resource.value ?? base.value)
  }

  async function markSaved(next: SettingsResource): Promise<void> {
    reset(next)
    savedAt.value = new Date(now().getTime())
    queryClient.setQueryData(settingsQueryKey(), next)
    await applyInvalidationPlan(queryClient, mutationInvalidationPlans.settings.update())
  }

  async function saveAll(extra: SettingsPatch = {}): Promise<void> {
    if (
      operationLocked.value ||
      !valid.value ||
      !base.value ||
      !draft.value ||
      (Object.keys(patch.value).length === 0 && Object.keys(extra).length === 0)
    ) {
      return
    }

    const normalizedPatch = {
      ...buildSettingsPatch(base.value.settings, draft.value, 'all'),
      ...extra,
    }
    requestController?.abort()
    const owner = ++requestOwner
    const controller = new AbortController()
    requestController = controller
    pending.value = true
    failed.value = false

    try {
      const response = await updateSettings(client, normalizedPatch, controller.signal)
      if (!isCurrent(owner, controller)) return
      await queryClient.cancelQueries({ queryKey: settingsQueryKey(), exact: true })
      if (!isCurrent(owner, controller)) return
      await markSaved(response)
    } catch (error: unknown) {
      if (!isCurrent(owner, controller) || error instanceof RequestCancelledError) return
      failed.value = true
    } finally {
      if (isCurrent(owner, controller)) {
        requestController = undefined
        pending.value = false
        consumeCurrentResource()
      }
    }
  }

  onBeforeUnmount(() => {
    mounted = false
    requestOwner += 1
    requestController?.abort()
    requestController = undefined
  })

  return {
    base,
    draft,
    patch,
    dirty,
    valid,
    pending,
    failed,
    operationLocked,
    savedAt,
    updateDraft,
    discard,
    saveAll,
  }
}
