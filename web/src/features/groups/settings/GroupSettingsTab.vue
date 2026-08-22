<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import type { GroupSettingsDto, HeaderRulesDto, ProxyMutation } from '@/api/control/types'

import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { channelsQueryOptions, type ChannelFieldDto } from '@/app/resources/channels'
import {
  cacheGroupSettings,
  groupSettingsQueryOptions,
  invalidateGroupSettingsDependents,
  updateGroupSettings,
} from '@/app/resources/groups'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import { useTransientFlag } from '@/app/use-transient-flag'
import { groupDetailLocation } from '@/app/route-locations'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import ProxyConfigEditor from '@/components/config/ProxyConfigEditor.vue'
import RuntimeOverrideRow from '@/components/config/RuntimeOverrideRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import SectionNav from '@/components/ui/SectionNav.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'
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
import {
  parseGroupSettingsRouteQuery,
  serializeGroupSettingsRouteQuery,
  type GroupSettingsRouteState,
  type GroupSettingsSection,
  normalizeGroupTab,
} from '../group-route'

const props = defineProps<{ groupId: number }>()
const client = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const routeState = computed(() => parseGroupSettingsRouteQuery(route.query))
const query = useQuery(groupSettingsQueryOptions(client, () => props.groupId))
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const initialLoading = useStableLoading(
  () => query.isPending.value && query.data.value === undefined,
)
const queryRefreshing = computed(() => query.data.value !== undefined && query.isFetching.value)
const saved = ref<GroupSettingsDto>()
const draft = ref<GroupSettingsDraft>()
const pending = ref(false)
const proxyPending = ref(false)
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
const selectedChannel = computed(() =>
  channelsQuery.data.value?.items.find(({ channel_id }) => channel_id === draft.value?.channel_id),
)
const channelName = computed(() => selectedChannel.value?.name ?? draft.value?.channel_id ?? '')
const channelParamFields = computed<ChannelFieldDto[]>(() =>
  saved.value?.connection_type === 'subscription'
    ? []
    : (selectedChannel.value?.param_fields ?? []),
)
const channelParamsDisabled = computed(() => selectedChannel.value === undefined)
let controller: AbortController | undefined
const navItems = computed(() => [
  { id: 'settings-general', label: t('group.settings.sections.general') },
  { id: 'settings-routing', label: t('group.settings.sections.routing') },
  { id: 'settings-runtime', label: t('group.settings.sections.runtime') },
  { id: 'settings-headers', label: t('group.settings.sections.headers') },
  { id: 'settings-danger', label: t('group.settings.sections.danger') },
])
const { activeSection: section, selectSection: scrollToSection } = useSectionNavigation({
  ids: computed(() => navItems.value.map(({ id }) => id)),
  initialId: `settings-${routeState.value.section}`,
  topOffset: 88,
})
const patch = computed(() =>
  saved.value && draft.value ? buildGroupSettingsPatch(saved.value, draft.value) : {},
)
const dirty = computed(
  () => !deleted.value && (Object.keys(patch.value).length > 0 || headerRulesInvalidEdits.value),
)
const mutationPending = computed(() => pending.value || proxyPending.value || deletePending.value)
const nameError = computed(() =>
  draft.value?.name.trim() ? '' : t('group.settings.base.nameError'),
)
const paramErrors = computed<Record<string, string>>(() => {
  const result: Record<string, string> = {}
  for (const field of channelParamFields.value) {
    const value = draft.value?.params[field.key]?.trim() ?? ''
    if (field.required && !value) {
      result[field.key] = t('group.settings.base.paramRequired', { field: field.label })
    } else if (field.input_kind === 'url' && value && !isValidUpstreamBaseURL(value)) {
      result[field.key] = t('group.settings.base.upstreamUrlError')
    }
  }
  return result
})
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
    Object.keys(paramErrors.value).length === 0 &&
    weightValid.value &&
    timeoutValid.value &&
    headerRulesValid.value,
)
const showInjectUsage = computed(
  () => selectedChannel.value?.client_protocols.includes('openai-completions') ?? false,
)
const displayedHeaderRules = computed<HeaderRulesDto>(
  () =>
    draft.value?.overrides.header_rules ??
    saved.value?.effective.header_rules ?? { set: {}, remove: [] },
)
const affinityMode = computed(() => {
  const value = draft.value?.overrides.affinity_enabled
  return value === undefined ? 'inherit' : value ? 'enabled' : 'disabled'
})
const affinityOptions = computed<SegmentedControlOption[]>(() => [
  {
    value: 'inherit',
    label: t('group.settings.runtime.affinityInherit'),
    disabled: mutationPending.value,
  },
  {
    value: 'enabled',
    label: t('group.settings.runtime.affinityEnable'),
    disabled: mutationPending.value,
  },
  {
    value: 'disabled',
    label: t('group.settings.runtime.affinityDisable'),
    disabled: mutationPending.value,
  },
])
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

useUnsavedChanges(dirty, {
  blocked: mutationPending,
  allowRouteUpdate: (to, from) =>
    to.name === from.name &&
    String(to.params.id) === String(from.params.id) &&
    normalizeGroupTab(to.query.tab) === 'settings' &&
    normalizeGroupTab(from.query.tab) === 'settings',
})

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

watch(
  () => routeState.value.section,
  (value) => {
    void nextTick(() => scrollToSection(`settings-${value}`))
  },
  { immediate: true },
)

function updateRoute(patch: Partial<GroupSettingsRouteState>, replace = false): void {
  const state = { ...routeState.value, ...patch }
  const location = groupDetailLocation(props.groupId, serializeGroupSettingsRouteQuery(state))
  void (replace ? router.replace(location) : router.push(location))
}

function sectionFromID(id: string): GroupSettingsSection | undefined {
  const value = id.replace(/^settings-/u, '')
  return value === 'general' ||
    value === 'routing' ||
    value === 'runtime' ||
    value === 'headers' ||
    value === 'danger'
    ? value
    : undefined
}

function setSection(id: string): void {
  const value = sectionFromID(id)
  if (value === undefined) return
  scrollToSection(id)
  if (value !== routeState.value.section) updateRoute({ section: value })
}

function setHeaderRulesExpanded(event: Event): void {
  const expanded = (event.currentTarget as HTMLDetailsElement).open
  if (expanded !== routeState.value.headerRulesExpanded) {
    updateRoute({ headerRulesExpanded: expanded })
  }
}

function updateParam(key: string, value: string | null): void {
  if (!draft.value) return
  const params = { ...draft.value.params }
  if (value === null) delete params[key]
  else params[key] = value
  draft.value = { ...draft.value, params }
}

function setTimeoutOverride(key: GroupTimeoutKey, enabled: boolean): void {
  if (!draft.value || !saved.value) return
  draft.value = setGroupConfigOverride(draft.value, key, enabled, saved.value.effective[key])
}

function setTimeoutValue(key: GroupTimeoutKey, value: string): void {
  if (!draft.value) return
  const overrides = {
    ...draft.value.overrides,
    [key]: Number(value),
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

function setAffinityMode(value: string): void {
  if (!draft.value || !['inherit', 'enabled', 'disabled'].includes(value)) return
  const overrides = { ...draft.value.overrides }
  if (value === 'inherit') delete overrides.affinity_enabled
  else overrides.affinity_enabled = value === 'enabled'
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

async function saveGroupProxy(value: ProxyMutation): Promise<void> {
  if (!saved.value || mutationPending.value) throw new Error('GROUP_PROXY_UNAVAILABLE')

  const active = new AbortController()
  controller = active
  proxyPending.value = true
  try {
    const result = await updateGroupSettings(client, props.groupId, { proxy: value }, active.signal)
    if (controller !== active) throw new RequestCancelledError()
    saved.value = { ...saved.value, proxy: result.proxy }
    cacheGroupSettings(queryClient, props.groupId, result)
    await invalidateGroupSettingsDependents(queryClient, props.groupId)
  } finally {
    if (controller === active) {
      controller = undefined
      proxyPending.value = false
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

    <AsyncRefreshIndicator :active="queryRefreshing" :label="t('group.settings.loading')" />

    <SkeletonSurface
      v-if="(query.isPending.value && !query.data.value) || initialLoading"
      variant="form"
      :concealed="!initialLoading"
      :label="t('group.settings.loading')"
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
      <InlineFeedback v-if="channelParamsDisabled" tone="danger">
        {{ t('group.settings.base.channelCatalogUnavailable') }}
        <AppButton variant="secondary" size="sm" @click="channelsQuery.refetch()">
          {{ t('common.retry') }}
        </AppButton>
      </InlineFeedback>
      <div class="group-settings__layout">
        <SectionNav
          :model-value="section"
          :items="navItems"
          :label="t('group.settings.sectionNav')"
          :caption="t('group.settings.sectionLabel')"
          appearance="ledger"
          @update:model-value="setSection"
        />
        <div class="group-settings__content">
          <GroupSettingsBaseForm
            section="general"
            :channel-id="draft.channel_id"
            :channel-name="channelName"
            :channel-icon="selectedChannel?.icon ?? ''"
            :channel-mark="selectedChannel?.mark ?? ''"
            :param-fields="channelParamFields"
            :params="draft.params"
            :name="draft.name"
            :validation-model="draft.validation_model"
            :weight-manual="draft.weight_manual"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :params-disabled="channelParamsDisabled"
            :name-error="nameError"
            :param-errors="paramErrors"
            @update:param="updateParam"
            @update:name="draft.name = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @update:enabled="draft.enabled = $event"
          />
          <GroupSettingsBaseForm
            section="routing"
            :channel-id="draft.channel_id"
            :channel-name="channelName"
            :channel-icon="selectedChannel?.icon ?? ''"
            :channel-mark="selectedChannel?.mark ?? ''"
            :param-fields="channelParamFields"
            :params="draft.params"
            :name="draft.name"
            :validation-model="draft.validation_model"
            :weight-manual="draft.weight_manual"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :params-disabled="channelParamsDisabled"
            :name-error="nameError"
            :param-errors="paramErrors"
            @update:param="updateParam"
            @update:name="draft.name = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @update:enabled="draft.enabled = $event"
          />
          <section id="settings-runtime" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.runtime') }}</h3>
              <p>{{ t('group.settings.runtime.description') }}</p>
            </header>
            <div class="group-settings__runtime">
              <div class="group-settings__runtime-row">
                <ProxyConfigEditor
                  scope="group"
                  :view="saved.proxy"
                  :save-proxy="saveGroupProxy"
                  :supported="selectedChannel?.capabilities.outbound_proxy"
                  :disabled="mutationPending || selectedChannel === undefined"
                />
              </div>
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
                    <div class="group-settings__runtime-input">
                      <AppTextInput
                        type="number"
                        min="1"
                        :model-value="String(draft.overrides[key])"
                        :label="
                          t('group.settings.runtime.valueFor', {
                            field: t(`group.settings.runtime.${key}`),
                          })
                        "
                        appearance="surface"
                        size="compact"
                        monospace
                        :disabled="mutationPending"
                        @update:model-value="setTimeoutValue(key, $event)"
                      />
                      <span aria-hidden="true">{{ t('group.settings.runtime.seconds') }}</span>
                    </div>
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
                    <div class="group-settings__boolean-value">
                      <AppSwitch
                        :model-value="draft.overrides.inject_usage_options"
                        :disabled="mutationPending"
                        :label="t('group.settings.runtime.inject_usage_options')"
                        @update:model-value="draft.overrides.inject_usage_options = $event"
                      />
                      <span>
                        {{
                          draft.overrides.inject_usage_options
                            ? t('group.settings.runtime.enabledValue')
                            : t('group.settings.runtime.disabledValue')
                        }}
                      </span>
                    </div>
                  </template>
                </RuntimeOverrideRow>
              </div>
              <div class="group-settings__runtime-row group-settings__affinity-row">
                <div class="group-settings__affinity-identity">
                  <strong>{{ t('group.settings.runtime.affinity_enabled') }}</strong>
                  <small>{{ t('group.settings.runtime.affinityHelp') }}</small>
                </div>
                <SegmentedControl
                  :model-value="affinityMode"
                  :options="affinityOptions"
                  :label="t('group.settings.runtime.affinity_enabled')"
                  size="sm"
                  @update:model-value="setAffinityMode"
                />
              </div>
            </div>
          </section>
          <section id="settings-headers" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.headers') }}</h3>
              <p>{{ t('group.settings.headers.description') }}</p>
            </header>
            <details
              class="group-settings__header-rules"
              :open="routeState.headerRulesExpanded"
              @toggle="setHeaderRulesExpanded"
            >
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
  border-top: 1px solid var(--color-border-subtle);
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
  font-size: var(--text-body);
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
}
.group-settings__runtime-row {
  min-width: 0;
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
  min-height: var(--control-xs);
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-meta);
  white-space: nowrap;
}
.group-settings__affinity-row {
  display: flex;
  min-height: 68px;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: 11px 2px;
}
.group-settings__affinity-identity {
  display: grid;
  min-width: 0;
  gap: var(--space-1);
}
.group-settings__affinity-identity strong {
  font-size: var(--text-meta);
}
.group-settings__affinity-identity small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
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
  .group-settings__runtime-input {
    width: min(100%, 220px);
  }
  .group-settings__danger-zone {
    align-items: stretch;
    flex-direction: column;
  }
  .group-settings__affinity-row {
    align-items: stretch;
    flex-direction: column;
  }
}
</style>
