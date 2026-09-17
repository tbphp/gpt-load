import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getSettings,
  saveSettings,
  settingsKey,
  settingNumbers,
  type SettingNumber,
  type SettingKey,
  type SettingsData,
} from '@modern/api/settings'
import { useMessages, useMessageSource } from '@modern/app/messages'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import {
  buildSettingsPatch,
  changedSettings,
  cloneDraft,
  createSettingsDraft,
  settingsErrors,
  type SettingsDraft,
} from './settings-draft'

export function useSettingsEditor() {
  const { t, n } = useI18n()
  const client = useApiClient()
  const cache = useQueryClient()
  const messages = useMessages()
  const query = useQuery({
    queryKey: settingsKey,
    queryFn: ({ signal }) => getSettings(client, signal),
    gcTime: 0,
  })
  const base = ref<SettingsData>()
  const baseline = ref<SettingsDraft>()
  const draft = ref<SettingsDraft>()
  const resets = ref(new Set<SettingKey>())
  const saving = ref(false)
  const attempted = ref(false)
  const saveError = ref('')
  const controller = new AbortController()
  const changed = computed(() =>
    baseline.value && draft.value ? changedSettings(baseline.value, draft.value, resets.value) : [],
  )
  const dirty = computed(() => changed.value.length > 0)
  const effectiveBase = computed(() =>
    base.value
      ? { ...base.value, readOnly: query.data.value?.readOnly ?? base.value.readOnly }
      : undefined,
  )
  const errors = computed<Record<string, string>>(() =>
    effectiveBase.value && draft.value
      ? settingsErrors(effectiveBase.value, draft.value, changed.value, resets.value)
      : {},
  )
  const fieldErrors = computed<Record<string, string>>(() =>
    attempted.value
      ? Object.fromEntries(
          Object.entries(errors.value).map(([key, code]) => {
            const range = key in settingNumbers ? settingNumbers[key as SettingNumber] : undefined
            return [
              key,
              range
                ? t('settingsForm.errors.number', { min: n(range.min), max: n(range.max) })
                : t('settingsForm.errors.' + code),
            ]
          }),
        )
      : {},
  )

  function load(data: SettingsData): void {
    base.value = data
    baseline.value = createSettingsDraft(data)
    draft.value = cloneDraft(baseline.value)
    resets.value = new Set()
    attempted.value = false
    saveError.value = ''
  }
  watch(
    query.data,
    (data) => {
      if (data && !dirty.value && !saving.value) load(data)
    },
    { immediate: true },
  )
  watch(dirty, (value) => {
    if (!value && !saving.value && query.data.value && query.data.value !== base.value)
      load(query.data.value)
  })
  function discard(): void {
    if (!saving.value && (query.data.value ?? base.value)) load((query.data.value ?? base.value)!)
  }
  function owned(key: SettingKey): boolean {
    return Boolean(
      base.value &&
      (key === 'proxy_config'
        ? base.value.values.proxy_config.configured_mode !== 'inherit'
        : base.value.overrides.includes(key)),
    )
  }
  function locked(key: SettingKey): boolean {
    return Boolean(effectiveBase.value?.readOnly.includes(key))
  }
  function restore(key: SettingKey): void {
    if (!baseline.value || !draft.value || saving.value || locked(key)) return
    draft.value = { ...draft.value, [key]: cloneDraft(baseline.value[key]) }
    if (owned(key)) resets.value.add(key)
    else resets.value.delete(key)
  }
  function undoRestore(key: SettingKey): void {
    if (!saving.value && !locked(key)) resets.value.delete(key)
  }
  async function save(): Promise<'invalid' | 'saved' | undefined> {
    if (!effectiveBase.value || !draft.value || saving.value || !dirty.value) return
    attempted.value = true
    if (Object.keys(errors.value).length) return 'invalid'
    const patch = buildSettingsPatch(effectiveBase.value, draft.value, changed.value, resets.value)
    if (!Object.keys(patch).length) {
      discard()
      return
    }
    saving.value = true
    saveError.value = ''
    try {
      await cache.cancelQueries({ queryKey: settingsKey })
      const data = await saveSettings(client, patch, controller.signal)
      if (controller.signal.aborted) return
      // 保存期间可能发生页面可见性刷新；写入确认结果前先取消旧读取。
      await cache.cancelQueries({ queryKey: settingsKey })
      if (controller.signal.aborted) return
      cache.setQueryData(settingsKey, data)
      load(data)
      await Promise.all([
        cache.invalidateQueries({ queryKey: ['modern', 'groups'], refetchType: 'none' }),
        cache.invalidateQueries({ queryKey: ['modern', 'group-settings'], refetchType: 'none' }),
        cache.invalidateQueries({ queryKey: ['modern', 'models'], refetchType: 'none' }),
      ])
      if (!controller.signal.aborted)
        messages.show({ tone: 'success', text: t('settingsForm.saved') })
      return 'saved'
    } catch {
      if (!controller.signal.aborted) saveError.value = t('settingsForm.saveFailed')
    } finally {
      saving.value = false
    }
  }
  useLoadingActivity(() => saving.value || query.isFetching.value)
  useMessageSource(() =>
    saveError.value
      ? { tone: 'danger', text: saveError.value }
      : query.isError.value && base.value
        ? { tone: 'warning', text: t('settingsForm.stale') }
        : undefined,
  )
  onScopeDispose(() => {
    controller.abort()
    draft.value = undefined
    baseline.value = undefined
    base.value = undefined
  })
  return {
    query,
    base,
    draft,
    dirty,
    changed,
    resets,
    saving,
    attempted,
    fieldErrors,
    owned,
    locked,
    restore,
    undoRestore,
    discard,
    save,
  }
}
