import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, toValue, watch, type MaybeRefOrGetter } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getModelProfile,
  modelProfileFields,
  modelProfileKey,
  modelsKey,
  saveModelProfile,
  type ModelProfile,
  type ModelProfileField,
} from '@modern/api/models'
import { useMessages } from '@modern/app/messages'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import {
  createModelProfileDraft,
  modelProfileDraftErrors,
  modelProfileDraftOverrides,
  resetModelProfileDraft,
  setModelProfileFieldMode,
  type ModelProfileDraft,
} from './model-profile-draft'

export function useModelProfileEditor(model: MaybeRefOrGetter<string>) {
  const { t } = useI18n()
  const client = useApiClient()
  const cache = useQueryClient()
  const messages = useMessages()
  const modelName = computed(() => toValue(model))
  const query = useQuery(
    computed(() => ({
      queryKey: modelProfileKey(modelName.value),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        getModelProfile(client, modelName.value, signal),
      gcTime: 0,
    })),
  )
  const base = ref<ModelProfile>()
  const draft = ref<ModelProfileDraft>()
  const saving = ref(false)
  const attempted = ref(false)
  const saveError = ref('')
  const controller = new AbortController()
  const overrides = computed(() => (draft.value ? modelProfileDraftOverrides(draft.value) : {}))
  const dirty = computed(
    () =>
      Boolean(base.value && draft.value) &&
      JSON.stringify(overrides.value) !== JSON.stringify(base.value?.overrides ?? {}),
  )
  const errors = computed(() => (draft.value ? modelProfileDraftErrors(draft.value) : {}))
  const fieldErrors = computed<Partial<Record<ModelProfileField, string>>>(() =>
    attempted.value
      ? Object.fromEntries(
          Object.entries(errors.value).map(([field, error]) => [
            field,
            t(`modelManager.profile.errors.${error}`),
          ]),
        )
      : {},
  )
  const customCount = computed(() =>
    draft.value ? modelProfileFields.filter((field) => draft.value?.custom[field]).length : 0,
  )

  function load(profile: ModelProfile): void {
    base.value = profile
    draft.value = createModelProfileDraft(profile)
    attempted.value = false
    saveError.value = ''
  }

  watch(
    query.data,
    (profile) => {
      if (profile && !dirty.value && !saving.value) load(profile)
    },
    { immediate: true },
  )
  watch(
    draft,
    () => {
      if (saveError.value) saveError.value = ''
    },
    { deep: true },
  )

  function setCustom(field: ModelProfileField, custom: boolean): void {
    if (!base.value || !draft.value || saving.value) return
    setModelProfileFieldMode(base.value, draft.value, field, custom)
  }

  function resetAll(): void {
    if (!base.value || !draft.value || saving.value) return
    resetModelProfileDraft(base.value, draft.value)
  }

  async function save(): Promise<'invalid' | 'saved' | undefined> {
    if (!base.value || !draft.value || saving.value || !dirty.value) return
    attempted.value = true
    if (Object.keys(errors.value).length) return 'invalid'
    saving.value = true
    saveError.value = ''
    const name = modelName.value
    const key = modelProfileKey(name)
    try {
      await cache.cancelQueries({ queryKey: key })
      const profile = await saveModelProfile(client, name, overrides.value, controller.signal)
      if (controller.signal.aborted) return
      await cache.cancelQueries({ queryKey: key })
      if (controller.signal.aborted) return
      cache.setQueryData(key, profile)
      load(profile)
      await Promise.all([
        cache.invalidateQueries({ queryKey: [...modelsKey, 'collection'] }),
        cache.invalidateQueries({ queryKey: [...modelsKey, 'context'] }),
      ])
      if (!controller.signal.aborted)
        messages.show({ tone: 'success', text: t('modelManager.profile.saveSuccess') })
      return 'saved'
    } catch {
      if (!controller.signal.aborted) saveError.value = t('modelManager.profile.saveFailed')
    } finally {
      saving.value = false
    }
  }

  useLoadingActivity(() => saving.value || query.isFetching.value)
  onScopeDispose(() => controller.abort())

  return {
    query,
    base,
    draft,
    dirty,
    customCount,
    saving,
    saveError,
    fieldErrors,
    setCustom,
    resetAll,
    save,
  }
}
