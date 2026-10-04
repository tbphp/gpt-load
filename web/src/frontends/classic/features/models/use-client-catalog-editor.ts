import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  clientCatalogKey,
  getClientCatalog,
  modelsKey,
  previewClientCatalog,
  saveClientCatalog,
  type ClientCatalog,
  type ClientCatalogBudget,
  type ClientCatalogDraft,
  type ModelProfileField,
} from '@/app/resources/client-catalog'
import { useApiClient } from '@shared/http/client-context'
import {
  createModelProfileDraft,
  modelProfileDraftErrors,
  modelProfileDraftOverrides,
  resetModelProfileDraft,
  type ModelProfileDraft,
} from './model-profile-draft'

export function useClientCatalogEditor() {
  const { t } = useI18n()
  const client = useApiClient()
  const cache = useQueryClient()
  const query = useQuery({
    queryKey: clientCatalogKey,
    queryFn: ({ signal }) => getClientCatalog(client, signal),
    gcTime: 0,
  })
  const base = shallowRef<ClientCatalog>()
  const selected = ref<string[]>([])
  const drafts = ref<Record<string, ModelProfileDraft>>({})
  const useDefaults = ref(false)
  const attempted = ref(false)
  const saving = ref(false)
  const saved = ref(false)
  const saveError = ref('')
  const budget = shallowRef<ClientCatalogBudget>()
  const previewing = ref(false)
  const previewError = ref(false)
  const controller = new AbortController()
  let previewController: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  const profiles = computed(
    () => new Map(base.value?.models.map((model) => [model.clientModel, model]) ?? []),
  )
  const updates = computed(() =>
    (base.value?.models ?? []).flatMap((profile) => {
      const draft = drafts.value[profile.clientModel]
      if (!draft) return []
      const overrides = modelProfileDraftOverrides(draft)
      return JSON.stringify(overrides) === JSON.stringify(profile.overrides)
        ? []
        : [{ client_model: profile.clientModel, overrides }]
    }),
  )
  const directoryChanged = computed(
    () => JSON.stringify(selected.value) !== JSON.stringify(base.value?.selected),
  )
  const payload = computed<ClientCatalogDraft>(() => ({
    known_models: base.value?.models.map((model) => model.clientModel) ?? [],
    ...(useDefaults.value
      ? { reset_directory: true }
      : directoryChanged.value
        ? { models: [...selected.value] }
        : {}),
    profiles: updates.value,
  }))
  const requestKey = computed(() => JSON.stringify(payload.value))
  const dirty = computed(
    () =>
      Boolean(base.value) &&
      (useDefaults.value || updates.value.length > 0 || directoryChanged.value),
  )
  const errors = computed(() =>
    Object.fromEntries(
      Object.entries(drafts.value).map(([name, draft]) => [name, modelProfileDraftErrors(draft)]),
    ),
  )
  const invalidModel = computed(() =>
    Object.keys(errors.value).find((name) => Object.keys(errors.value[name] ?? {}).length),
  )
  const editedOutside = computed(() =>
    updates.value
      .map((update) => update.client_model)
      .filter((name) => !selected.value.includes(name)),
  )

  function fieldErrors(name: string): Partial<Record<ModelProfileField, string>> {
    return attempted.value
      ? Object.fromEntries(
          Object.entries(errors.value[name] ?? {}).map(([field, error]) => [
            field,
            t(`models.profile.errors.${error}`),
          ]),
        )
      : {}
  }
  function load(value: ClientCatalog): void {
    base.value = value
    selected.value = [...value.selected]
    drafts.value = Object.fromEntries(
      value.models.map((profile) => [profile.clientModel, createModelProfileDraft(profile)]),
    )
    useDefaults.value = false
    attempted.value = false
    saveError.value = ''
    budget.value = value.budget
    previewError.value = false
  }
  watch(
    query.data,
    (value) => {
      if (value && !dirty.value && !saving.value) load(value)
    },
    { immediate: true },
  )
  async function preview(key: string): Promise<void> {
    previewController = new AbortController()
    const signal = previewController.signal
    try {
      const value = await previewClientCatalog(
        client,
        JSON.parse(key) as ClientCatalogDraft,
        signal,
      )
      if (!signal.aborted && key === requestKey.value) {
        budget.value = value.budget
        previewError.value = false
      }
    } catch {
      if (!signal.aborted && key === requestKey.value) previewError.value = true
    } finally {
      if (!signal.aborted && key === requestKey.value) previewing.value = false
    }
  }
  function schedulePreview(): void {
    if (saving.value) return
    clearTimeout(timer)
    previewController?.abort()
    saveError.value = ''
    saved.value = false
    previewing.value = Boolean(base.value && !invalidModel.value)
    if (!previewing.value) return
    const key = requestKey.value
    timer = setTimeout(() => void preview(key), 200)
  }
  watch([requestKey, invalidModel], schedulePreview)
  function restoreDirectory(): void {
    if (!base.value || saving.value) return
    useDefaults.value = true
    selected.value = [...base.value.defaults]
  }
  function add(names: string[]): void {
    if (saving.value) return
    useDefaults.value = false
    selected.value = [
      ...selected.value,
      ...names.filter((name) => profiles.value.has(name) && !selected.value.includes(name)),
    ]
  }
  function remove(name: string): void {
    if (saving.value) return
    useDefaults.value = false
    selected.value = selected.value.filter((value) => value !== name)
  }
  function move(name: string, target: number): void {
    if (saving.value) return
    const index = selected.value.indexOf(name)
    if (index < 0 || target < 0 || target >= selected.value.length || index === target) return
    useDefaults.value = false
    const next = [...selected.value]
    next.splice(index, 1)
    next.splice(target, 0, name)
    selected.value = next
  }
  function resetProfile(name: string): void {
    const profile = profiles.value.get(name)
    const draft = drafts.value[name]
    if (profile && draft && !saving.value) resetModelProfileDraft(profile, draft)
  }
  async function save(): Promise<string | undefined> {
    attempted.value = true
    if (invalidModel.value) return invalidModel.value
    if (!dirty.value || saving.value) return
    saving.value = true
    clearTimeout(timer)
    previewController?.abort()
    previewing.value = false
    try {
      const value = await saveClientCatalog(client, payload.value, controller.signal)
      if (controller.signal.aborted) return
      load(value)
      await cache.cancelQueries({ queryKey: clientCatalogKey })
      cache.setQueryData(clientCatalogKey, value)
      await cache.invalidateQueries({ queryKey: [...modelsKey, 'collection'] })
      saved.value = true
    } catch {
      if (!controller.signal.aborted) {
        saving.value = false
        schedulePreview()
        saveError.value = t('models.clientCatalog.saveFailed')
      }
    } finally {
      saving.value = false
    }
  }
  onScopeDispose(() => {
    clearTimeout(timer)
    previewController?.abort()
    controller.abort()
  })
  return {
    query,
    base,
    selected,
    drafts,
    profiles,
    dirty,
    saving,
    saved,
    previewing,
    saveError,
    budget,
    previewError,
    editedOutside,
    fieldErrors,
    restoreDirectory,
    add,
    remove,
    move,
    resetProfile,
    schedulePreview,
    save,
  }
}
