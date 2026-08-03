<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupSettingsDto, HeaderRulesDto } from '@/api/control/types'

import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import {
  cacheGroupSettings,
  groupSettingsQueryOptions,
  invalidateGroupSettingsDependents,
  updateGroupSettings,
} from '@/app/resources/groups'
import { providerSuggestionsQueryOptions } from '@/app/resources/providers'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import { useDebouncedAction } from '@/app/use-debounced-action'
import { useTransientFlag } from '@/app/use-transient-flag'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SectionNav from '@/components/ui/SectionNav.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'
import { useSectionNavigation } from '@/composables/use-section-navigation'
import { isValidUpstreamBaseURL } from '@/lib/upstream-base-url'

import GroupDeleteDialog from './GroupDeleteDialog.vue'
import GroupSettingsBaseForm from './GroupSettingsBaseForm.vue'
import {
  buildGroupSettingsPatch,
  createGroupSettingsDraft,
  groupTimeoutKeys,
  setGroupConfigOverride,
  type GroupSettingsDraft,
  type GroupTimeoutKey,
} from './group-settings-patch'

const props = defineProps<{ groupId: number }>()
const client = useApiClient()
const queryClient = useQueryClient()
const { t } = useI18n()
const query = useQuery(groupSettingsQueryOptions(client, () => props.groupId))
const saved = ref<GroupSettingsDto>()
const draft = ref<GroupSettingsDraft>()
const providerSearchInput = ref('')
const providerSearch = ref('')
const providerSearchDebounce = useDebouncedAction(250)
const providerQuery = useQuery(providerSuggestionsQueryOptions(client, providerSearch))
const pending = ref(false)
const deletePending = ref(false)
const deleted = ref(false)
const error = ref('')
const headerRulesValid = ref(true)
const headerRulesInvalidEdits = ref(false)
const headerRulesEditorRevision = ref(0)
const {
  value: savedFeedback,
  clear: clearSavedFeedback,
  show: showSavedFeedback,
} = useTransientFlag(1_600)
const timeoutKeys = groupTimeoutKeys
const customProviderValue = 'custom:'
const providerOptions = computed(() => {
  const items = providerQuery.data.value?.items ?? []
  const options = [
    { value: customProviderValue, label: t('group.settings.base.providerCustom') },
    ...items.map(({ provider_id, name }) => ({ value: provider_id, label: name })),
  ]
  const current = draft.value?.provider_id
  if (current && !items.some(({ provider_id }) => provider_id === current)) {
    options.push({ value: current, label: current })
  }
  return options
})

function setProviderSearch(value: string): void {
  providerSearchInput.value = value
  providerSearchDebounce.schedule(() => {
    providerSearch.value = value
  })
}
let controller: AbortController | undefined
const navItems = computed(() => [
  { id: 'settings-general', label: t('group.settings.sections.general') },
  { id: 'settings-routing', label: t('group.settings.sections.routing') },
  { id: 'settings-runtime', label: t('group.settings.sections.runtime') },
  { id: 'settings-headers', label: t('group.settings.sections.headers') },
  { id: 'settings-danger', label: t('group.settings.sections.danger') },
])
const { activeSection: section, selectSection: setSection } = useSectionNavigation({
  ids: computed(() => navItems.value.map(({ id }) => id)),
  initialId: 'settings-general',
  topOffset: 88,
})
const patch = computed(() =>
  saved.value && draft.value ? buildGroupSettingsPatch(saved.value, draft.value) : {},
)
const dirty = computed(
  () => !deleted.value && (Object.keys(patch.value).length > 0 || headerRulesInvalidEdits.value),
)
const mutationPending = computed(() => pending.value || deletePending.value)
const nameError = computed(() =>
  draft.value?.name.trim() ? '' : t('group.settings.base.nameError'),
)
const urlError = computed(() => {
  const value = draft.value?.upstream_url.trim() ?? ''
  if (!value || !isValidUpstreamBaseURL(value)) return t('group.settings.base.upstreamUrlError')
  return ''
})
const protocolsError = computed(() =>
  draft.value?.protocols.length ? '' : t('group.settings.base.protocolsError'),
)
const weightValid = computed(() => {
  const value = draft.value?.weight_manual
  return (
    value === null || (Number.isInteger(value) && value !== undefined && value >= 1 && value <= 100)
  )
})
const timeoutValid = computed(() =>
  timeoutKeys.every((key) => {
    const value = draft.value?.overrides[key]
    return value === undefined || (Number.isSafeInteger(value) && value > 0)
  }),
)
const valid = computed(
  () =>
    !nameError.value &&
    !urlError.value &&
    !protocolsError.value &&
    weightValid.value &&
    timeoutValid.value &&
    headerRulesValid.value,
)
const showInjectUsage = computed(
  () => draft.value?.protocols.includes('openai-completions') ?? false,
)
const displayedHeaderRules = computed<HeaderRulesDto>(
  () =>
    draft.value?.overrides.header_rules ??
    saved.value?.effective.header_rules ?? { set: {}, remove: [] },
)
function resetSavedDraft(settings: GroupSettingsDto): void {
  saved.value = settings
  draft.value = createGroupSettingsDraft(settings)
  headerRulesValid.value = true
  headerRulesInvalidEdits.value = false
  headerRulesEditorRevision.value += 1
}

function consumeCurrentQuery(): void {
  const latest = query.data.value
  if (!latest || latest === saved.value || dirty.value || mutationPending.value || deleted.value)
    return
  resetSavedDraft(latest)
}

useUnsavedChanges(dirty, { blocked: mutationPending })

watch(
  () => query.data.value,
  () => {
    consumeCurrentQuery()
  },
  { immediate: true },
)

watch(dirty, (isDirty) => {
  if (isDirty) clearSavedFeedback()
  else consumeCurrentQuery()
})

watch([mutationPending, deleted], () => {
  consumeCurrentQuery()
})

function toggleProtocol(protocol: GroupSettingsDraft['protocols'][number], checked: boolean): void {
  if (!draft.value) return
  const protocols = checked
    ? [...new Set([...draft.value.protocols, protocol])]
    : draft.value.protocols.filter((value) => value !== protocol)
  const overrides = { ...draft.value.overrides }
  if (!protocols.includes('openai-completions')) delete overrides.inject_usage_options
  draft.value = { ...draft.value, protocols, overrides }
}

function setTimeoutOverride(key: GroupTimeoutKey, enabled: boolean): void {
  if (!draft.value || !saved.value) return
  draft.value = setGroupConfigOverride(draft.value, key, enabled, saved.value.effective[key])
}

function setTimeoutValue(key: GroupTimeoutKey, event: Event): void {
  if (!draft.value) return
  const overrides = {
    ...draft.value.overrides,
    [key]: Number((event.target as HTMLInputElement).value),
  }
  draft.value = { ...draft.value, overrides }
}

function updateHeaderRules(value: HeaderRulesDto): void {
  if (!draft.value) return
  draft.value = {
    ...draft.value,
    overrides: {
      ...draft.value.overrides,
      header_rules: { set: { ...value.set }, remove: [...value.remove] },
    },
  }
}

function setInjectUsageOverride(enabled: boolean): void {
  if (!draft.value || !saved.value) return
  const overrides = { ...draft.value.overrides }
  if (enabled) overrides.inject_usage_options = saved.value.effective.inject_usage_options
  else delete overrides.inject_usage_options
  draft.value = { ...draft.value, overrides }
}

function requestSave(): void {
  if (!dirty.value || !valid.value || mutationPending.value) return
  void save()
}

async function save(): Promise<void> {
  if (!saved.value || !draft.value || mutationPending.value || !valid.value) return
  const active = new AbortController()
  controller = active
  pending.value = true
  clearSavedFeedback()
  error.value = ''
  try {
    const body = patch.value
    const result = await updateGroupSettings(client, props.groupId, body, active.signal)
    if (controller !== active) return
    resetSavedDraft(result)
    cacheGroupSettings(queryClient, props.groupId, result)
    await invalidateGroupSettingsDependents(queryClient, props.groupId)
    showSavedFeedback()
  } catch (cause: unknown) {
    if (cause instanceof RequestCancelledError || controller !== active) return
    error.value = t('group.settings.saveFailed')
  } finally {
    if (controller === active) {
      controller = undefined
      pending.value = false
    }
  }
}

function discard(): void {
  if (!saved.value || mutationPending.value) return
  error.value = ''
  clearSavedFeedback()
  resetSavedDraft(saved.value)
  consumeCurrentQuery()
}

function onDeleted(): void {
  deleted.value = true
  error.value = ''
}

function headerSummary(): string {
  return t('group.settings.runtime.headerSummary', {
    set: Object.keys(displayedHeaderRules.value.set).length,
    remove: displayedHeaderRules.value.remove.length,
  })
}

onBeforeUnmount(() => {
  controller?.abort()
})
</script>

<template>
  <section class="group-settings" aria-labelledby="group-settings-heading">
    <PanelHeader heading-id="group-settings-heading" :title="t('group.settings.title')" />
    <QueryFeedback
      v-if="query.isPending.value && !query.data.value"
      state="loading"
      :message="t('group.settings.loading')"
    />
    <QueryFeedback
      v-else-if="query.isError.value && !query.data.value"
      state="error"
      :message="t('group.settings.loadFailed')"
      :retry-label="t('common.retry')"
      @retry="query.refetch()"
    />
    <template v-else-if="saved && draft">
      <InlineFeedback v-if="error" tone="danger">{{ error }}</InlineFeedback>
      <div class="group-settings__layout">
        <SectionNav
          v-model="section"
          :items="navItems"
          :label="t('group.settings.sectionNav')"
          :caption="t('group.settings.sectionLabel')"
          appearance="ledger"
          @update:model-value="setSection"
        />
        <div class="group-settings__content">
          <GroupSettingsBaseForm
            section="general"
            :provider-id="draft.provider_id"
            :provider-search="providerSearchInput"
            :provider-options="providerOptions"
            :provider-loading="providerQuery.isFetching.value"
            :provider-error="providerQuery.isError.value"
            :name="draft.name"
            :upstream-url="draft.upstream_url"
            :validation-model="draft.validation_model"
            :weight-manual="draft.weight_manual"
            :protocols="draft.protocols"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :name-error="nameError"
            :upstream-url-error="urlError"
            :protocols-error="protocolsError"
            @update:provider-id="draft.provider_id = $event"
            @update:provider-search="setProviderSearch"
            @update:name="draft.name = $event"
            @update:upstream-url="draft.upstream_url = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @toggle-protocol="toggleProtocol"
            @update:enabled="draft.enabled = $event"
          />
          <GroupSettingsBaseForm
            section="routing"
            :provider-id="draft.provider_id"
            :provider-search="providerSearchInput"
            :provider-options="providerOptions"
            :provider-loading="providerQuery.isFetching.value"
            :provider-error="providerQuery.isError.value"
            :name="draft.name"
            :upstream-url="draft.upstream_url"
            :validation-model="draft.validation_model"
            :weight-manual="draft.weight_manual"
            :protocols="draft.protocols"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :name-error="nameError"
            :upstream-url-error="urlError"
            :protocols-error="protocolsError"
            @update:provider-id="draft.provider_id = $event"
            @update:provider-search="setProviderSearch"
            @update:name="draft.name = $event"
            @update:upstream-url="draft.upstream_url = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @toggle-protocol="toggleProtocol"
            @update:enabled="draft.enabled = $event"
          />
          <section id="settings-runtime" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.runtime') }}</h3>
              <p>{{ t('group.settings.runtime.description') }}</p>
            </header>
            <div class="group-settings__runtime">
              <div v-for="key in timeoutKeys" :key="key" class="group-settings__runtime-row">
                <RuntimeOverrideRow
                  appearance="ledger"
                  :label="t(`group.settings.runtime.${key}`)"
                  :detail="t('group.settings.runtime.effective', { value: saved.effective[key] })"
                  :value-label="t('group.settings.runtime.currentValue')"
                  :source-label="
                    draft.overrides[key] === undefined
                      ? t('group.settings.runtime.inherited')
                      : t('group.settings.runtime.override')
                  "
                  :action-label="
                    draft.overrides[key] === undefined
                      ? t('group.settings.runtime.useOverride')
                      : t('group.settings.runtime.useInherited')
                  "
                  :overridden="draft.overrides[key] !== undefined"
                  :disabled="mutationPending"
                  @toggle="setTimeoutOverride(key, draft.overrides[key] === undefined)"
                >
                  <template v-if="draft.overrides[key] !== undefined" #value>
                    <label class="group-settings__runtime-input">
                      <input
                        type="number"
                        min="1"
                        :aria-label="
                          t('group.settings.runtime.valueFor', {
                            field: t(`group.settings.runtime.${key}`),
                          })
                        "
                        :value="draft.overrides[key]"
                        :disabled="mutationPending"
                        @input="setTimeoutValue(key, $event)"
                      />
                      <span aria-hidden="true">{{ t('group.settings.runtime.seconds') }}</span>
                    </label>
                  </template>
                </RuntimeOverrideRow>
              </div>
              <div v-if="showInjectUsage" class="group-settings__runtime-row">
                <RuntimeOverrideRow
                  appearance="ledger"
                  :label="t('group.settings.runtime.inject_usage_options')"
                  :detail="t('group.settings.runtime.injectUsageHelp')"
                  :value-label="t('group.settings.runtime.currentValue')"
                  :source-label="
                    draft.overrides.inject_usage_options === undefined
                      ? t('group.settings.runtime.inherited')
                      : t('group.settings.runtime.override')
                  "
                  :action-label="
                    draft.overrides.inject_usage_options === undefined
                      ? t('group.settings.runtime.useOverride')
                      : t('group.settings.runtime.useInherited')
                  "
                  :overridden="draft.overrides.inject_usage_options !== undefined"
                  :disabled="mutationPending"
                  @toggle="
                    setInjectUsageOverride(draft.overrides.inject_usage_options === undefined)
                  "
                >
                  <template v-if="draft.overrides.inject_usage_options !== undefined" #value>
                    <label class="group-settings__boolean-value">
                      <input
                        type="checkbox"
                        :checked="draft.overrides.inject_usage_options"
                        :disabled="mutationPending"
                        @change="
                          draft.overrides.inject_usage_options = (
                            $event.target as HTMLInputElement
                          ).checked
                        "
                      />
                      {{
                        draft.overrides.inject_usage_options
                          ? t('group.settings.runtime.enabledValue')
                          : t('group.settings.runtime.disabledValue')
                      }}
                    </label>
                  </template>
                </RuntimeOverrideRow>
              </div>
            </div>
          </section>
          <section id="settings-headers" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.headers') }}</h3>
              <p>{{ t('group.settings.headers.description') }}</p>
            </header>
            <details class="group-settings__header-rules">
              <summary>
                <span>
                  <strong>{{ headerSummary() }}</strong>
                  <span>
                    {{
                      draft.overrides.header_rules === undefined
                        ? t('group.settings.runtime.inherited')
                        : t('group.settings.runtime.override')
                    }}
                  </span>
                </span>
                <ChevronDown :size="16" aria-hidden="true" />
              </summary>
              <div class="group-settings__header-controls">
                <HeaderRulesEditor
                  :key="headerRulesEditorRevision"
                  appearance="ledger"
                  :model-value="displayedHeaderRules"
                  :disabled="mutationPending"
                  :show-notice="false"
                  :remove-label="t('group.settings.runtime.headerRemove')"
                  :remove-hint="t('group.settings.runtime.headerRemoveHint')"
                  @update:valid="headerRulesValid = $event"
                  @update:invalid-edits="headerRulesInvalidEdits = $event"
                  @update:model-value="updateHeaderRules"
                />
              </div>
            </details>
          </section>
          <section id="settings-danger" class="group-settings__section group-settings__danger">
            <header>
              <h3>{{ t('group.settings.sections.danger') }}</h3>
              <p>{{ t('group.settings.dangerDescription') }}</p>
            </header>
            <div class="group-settings__danger-zone">
              <div>
                <strong>{{ t('group.settings.delete.open') }}</strong>
                <p>{{ t('group.settings.delete.sectionDescription') }}</p>
              </div>
              <GroupDeleteDialog
                :group-id="groupId"
                :group-name="saved.name"
                :disabled="mutationPending || deleted"
                @deleted="onDeleted"
                @update:pending="deletePending = $event"
              />
            </div>
          </section>
        </div>
      </div>
      <StickySaveBar
        appearance="ledger"
        always-visible
        :dirty="dirty"
        :pending="mutationPending"
        :status="error ? 'error' : savedFeedback ? 'saved' : 'idle'"
        :error="error"
        ><template #status
          ><div>
            <strong>
              {{
                pending
                  ? t('group.settings.saving')
                  : savedFeedback
                    ? t('group.settings.savedFeedback')
                    : dirty
                      ? t('group.settings.unsaved')
                      : t('group.settings.saved')
              }}
            </strong>
            <span>
              {{
                pending
                  ? t('group.settings.savingNote')
                  : savedFeedback
                    ? t('group.settings.savedFeedbackNote')
                    : dirty
                      ? t('group.settings.dirtyNote')
                      : t('group.settings.saveNote')
              }}
            </span>
          </div></template
        ><template #discard="{ disabled }"
          ><AppButton
            variant="ghost"
            size="sm"
            :disabled="disabled || !dirty || deletePending"
            @click="discard"
            >{{ t('common.discard') }}</AppButton
          ></template
        ><template #save="{ disabled }"
          ><AppButton
            size="sm"
            :disabled="disabled || !dirty || !valid || deletePending"
            @click="requestSave"
            >{{ t('group.settings.save') }}</AppButton
          ></template
        ></StickySaveBar
      >
    </template>
  </section>
</template>

<style scoped>
.group-settings {
  display: grid;
  gap: 0;
  min-width: 0;
  padding-top: var(--detail-panel-padding-top);
}
.group-settings__section header p,
small {
  color: var(--color-text-muted);
}
.group-settings__layout {
  display: grid;
  grid-template-columns: 176px minmax(0, 1fr);
  align-items: start;
  gap: 34px;
}
.group-settings__content {
  display: grid;
  min-width: 0;
  gap: var(--space-7);
}
.group-settings__section {
  display: grid;
  gap: 15px;
  scroll-margin-top: 76px;
  border-top: 1px solid var(--color-border-control);
  padding-top: 17px;
}
.group-settings__section:first-child {
  border-top: 0;
  padding-top: 0;
}
.group-settings__section header h3,
.group-settings__section header p {
  margin: 0;
}
.group-settings__section header h3 {
  font-size: 14px;
  font-weight: 650;
}
.group-settings__section header p {
  max-width: 580px;
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}
.group-settings__runtime {
  display: grid;
  border-top: 1px solid var(--color-border-subtle);
}
.group-settings__runtime-row {
  min-width: 0;
}
.group-settings__runtime-row input[type='number'] {
  width: 100%;
  min-height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 var(--space-2);
  font: var(--text-sm) var(--font-mono);
}
.group-settings__runtime-input {
  display: flex;
  width: min(100%, 190px);
  min-width: 0;
  align-items: center;
  gap: 7px;
}
.group-settings__runtime-input > span {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 11px;
  white-space: nowrap;
}
.group-settings__boolean-value {
  display: flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-2);
  white-space: nowrap;
}
.group-settings__header-rules {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
}
.group-settings__header-rules > summary {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  background: var(--color-surface-sunken);
  padding: 9px 12px;
  cursor: pointer;
  list-style: none;
}
.group-settings__header-rules > summary::-webkit-details-marker {
  display: none;
}
.group-settings__header-rules > summary > span {
  display: grid;
  gap: 1px;
}
.group-settings__header-rules > summary strong {
  font-size: var(--text-meta);
}
.group-settings__header-rules > summary span span {
  color: var(--color-text-faint);
  font-size: 11px;
}
.group-settings__header-rules > summary > svg {
  flex: none;
}
.group-settings__header-controls {
  display: grid;
  gap: 11px;
  border-top: 1px solid var(--color-border-subtle);
  padding: 12px;
}
.group-settings__danger-zone {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  border: 1px solid color-mix(in srgb, var(--color-danger) 42%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  padding: 13px 14px;
}
.group-settings__danger-zone strong {
  display: block;
  font-size: 12.5px;
}
.group-settings__danger-zone p {
  margin: 3px 0 0;
  color: var(--color-text-faint);
  font-size: 11px;
}
@media (max-width: 860px) {
  .group-settings {
    padding-top: var(--detail-panel-padding-top-compact);
  }
  .group-settings__layout {
    grid-template-columns: 1fr;
    gap: var(--space-5);
  }
}
@media (max-width: 800px) {
  .group-settings__runtime-row input[type='number'] {
    min-height: var(--touch-target);
    font-size: 16px;
  }
  .group-settings__runtime-input {
    width: min(100%, 220px);
  }
  .group-settings__danger-zone {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
