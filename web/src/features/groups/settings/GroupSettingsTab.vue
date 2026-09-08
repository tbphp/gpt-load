<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import type {
  AccessProtocol,
  GroupSettingsDto,
  HeaderRulesDto,
  ParameterOverrideRuleDto,
  ProxyConfiguredMode,
} from '@/api/control/types'

import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { useStableLoading } from '@/app/loading-state'
import { proxyDraftState, proxyOverrideToggleMode } from '@/app/resources/proxy'
import { channelsQueryOptions, type ChannelFieldDto } from '@/app/resources/channels'
import {
  cacheGroupSettings,
  groupModelsQueryOptions,
  groupSettingsQueryOptions,
  invalidateGroupSettingsDependents,
  updateGroupSettings,
} from '@/app/resources/groups'
import { useUnsavedChanges } from '@/app/unsaved-changes'
import { useTransientFlag } from '@/app/use-transient-flag'
import { groupDetailLocation } from '@/app/route-locations'
import HeaderRulesEditor from '@/components/config/HeaderRulesEditor.vue'
import ParameterOverrideRulesEditor from '@/components/config/ParameterOverrideRulesEditor.vue'
import ProxyOverrideControl from '@/components/config/ProxyOverrideControl.vue'
import SettingBlock from '@/components/config/SettingBlock.vue'
import SettingRow from '@/components/config/SettingRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import CompactFieldError from '@/components/ui/CompactFieldError.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import PanelHeader from '@/components/ui/PanelHeader.vue'
import QueryFeedback from '@/components/ui/QueryFeedback.vue'
import SkeletonSurface from '@/components/ui/SkeletonSurface.vue'
import SectionNav from '@/components/ui/SectionNav.vue'
import StickySaveBar from '@/components/ui/StickySaveBar.vue'
import { useSectionNavigation } from '@/composables/use-section-navigation'
import { isValidSubscriptionBaseURL, isValidUpstreamBaseURL } from '@/lib/upstream-base-url'
import { isValidPriceMultiplier } from '@/lib/price-multiplier'

import GroupDeleteDialog from './GroupDeleteDialog.vue'
import GroupSettingsBaseForm from './GroupSettingsBaseForm.vue'
import {
  buildGroupSettingsPatch,
  createGroupSettingsDraft,
  groupPolicyCountKeys,
  groupTimeoutKeys,
  setGroupConfigOverride,
  setGroupPolicyCountOverride,
  type GroupSettingsDraft,
  type GroupPolicyCountKey,
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
const modelsQuery = useQuery(groupModelsQueryOptions(client, () => props.groupId))
const channelsQuery = useQuery(channelsQueryOptions(client, ''))
const initialLoading = useStableLoading(
  () => query.isPending.value && query.data.value === undefined,
)
const queryRefreshing = computed(() => query.data.value !== undefined && query.isFetching.value)
const saved = ref<GroupSettingsDto>()
const draft = ref<GroupSettingsDraft>()
const pending = ref(false)
const deletePending = ref(false)
const deleted = ref(false)
const error = ref('')
const headerRulesValid = ref(true)
const headerRulesInvalidEdits = ref(false)
const headerRulesEditorRevision = ref(0)
const parameterOverridesValid = ref(true)
const parameterOverridesInvalidEdits = ref(false)
const parameterOverridesEditorRevision = ref(0)
const proxyMode = ref<ProxyConfiguredMode>('inherit')
const proxyEndpoint = ref('')
const {
  value: savedFeedback,
  clear: clearSavedFeedback,
  show: showSavedFeedback,
} = useTransientFlag(1_600)
const timeoutKeys = groupTimeoutKeys
const policyCountKeys = groupPolicyCountKeys
const policyRows = [
  {
    key: 'retry_count',
    helpKey: 'retryCountHelp',
  },
  {
    key: 'blacklist_threshold',
    helpKey: 'blacklistThresholdHelp',
  },
] as const
const selectedChannel = computed(() =>
  channelsQuery.data.value?.items.find(({ channel_id }) => channel_id === draft.value?.channel_id),
)
const channelParamFields = computed<ChannelFieldDto[]>(
  () => selectedChannel.value?.param_fields ?? [],
)
const channelParamsDisabled = computed(() => selectedChannel.value === undefined)
const parameterOverrideOperations = new Set([
  'chat_completion',
  'responses_create',
  'images_generate',
  'embeddings_create',
])
const parameterOverrideProtocols = computed<AccessProtocol[]>(() => [
  ...new Set(
    (selectedChannel.value?.routes ?? [])
      .filter(({ operation }) => parameterOverrideOperations.has(operation))
      .map(({ client_protocol }) => client_protocol),
  ),
])
let controller: AbortController | undefined
const navItems = computed(() => [
  { id: 'settings-general', label: t('group.settings.sections.general') },
  { id: 'settings-routing', label: t('group.settings.sections.routing') },
  { id: 'settings-runtime', label: t('group.settings.sections.runtime') },
  { id: 'settings-parameters', label: t('group.settings.sections.parameters') },
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
const proxyState = computed(() =>
  saved.value
    ? proxyDraftState(saved.value.proxy, proxyMode.value, proxyEndpoint.value)
    : { dirty: false, invalid: false, value: undefined },
)
// 代理沿用其它设置项的覆盖语义：inherit 即“继承全局”，direct/custom 即“本分组覆盖”。
const proxyOverridden = computed(() => proxyMode.value !== 'inherit')
const proxyPendingRestore = computed(
  () => saved.value?.proxy.configured_mode !== 'inherit' && proxyMode.value === 'inherit',
)
const proxyEffectiveLabel = computed(() => {
  const view = saved.value?.proxy
  if (!view) return ''
  return view.display_url ?? t(`common.proxy.mode.${view.effective_mode}`)
})
const proxySupported = computed(() => selectedChannel.value?.capabilities.outbound_proxy ?? false)
const proxyValue = computed(() => {
  if (!proxySupported.value) return t('common.proxy.unsupported')
  if (proxyPendingRestore.value) return t('group.settings.runtime.resetPending')
  return proxyEffectiveLabel.value
})

function toggleProxyOverride(): void {
  const base = saved.value?.proxy
  if (base) proxyMode.value = proxyOverrideToggleMode(base, proxyOverridden.value)
  proxyEndpoint.value = ''
}
const dirty = computed(
  () =>
    !deleted.value &&
    (Object.keys(patch.value).length > 0 ||
      headerRulesInvalidEdits.value ||
      parameterOverridesInvalidEdits.value ||
      proxyState.value.dirty),
)
const mutationPending = computed(() => pending.value || deletePending.value)
const nameError = computed(() =>
  draft.value?.name.trim() ? '' : t('group.settings.base.nameError'),
)
const paramErrors = computed<Record<string, string>>(() => {
  const result: Record<string, string> = {}
  for (const field of channelParamFields.value) {
    const value = draft.value?.params[field.key]?.trim() ?? ''
    const overrideRequired = field.key === 'base_url' && draft.value?.params.base_url !== undefined
    if ((field.required || overrideRequired) && !value) {
      result[field.key] = t('group.settings.base.paramRequired', {
        field: field.key === 'base_url' ? t('common.upstreamUrl.label') : field.label,
      })
    } else if (field.input_kind === 'url' && value) {
      const subscriptionBaseURL =
        draft.value?.connection_type === 'subscription' && field.key === 'base_url'
      const valid = subscriptionBaseURL
        ? isValidSubscriptionBaseURL(value)
        : isValidUpstreamBaseURL(value)
      if (!valid) {
        result[field.key] = t('common.upstreamUrl.invalid', {
          protocol: subscriptionBaseURL ? 'HTTPS' : 'HTTP(S)',
        })
      }
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
const policyCountsValid = computed(() =>
  policyCountKeys.every((key) => {
    const value = draft.value?.overrides[key]
    return value === undefined || (Number.isSafeInteger(value) && value >= 0)
  }),
)
const valid = computed(
  () =>
    !nameError.value &&
    Object.keys(paramErrors.value).length === 0 &&
    weightValid.value &&
    isValidPriceMultiplier(draft.value?.price_multiplier ?? '') &&
    timeoutValid.value &&
    policyCountsValid.value &&
    headerRulesValid.value &&
    parameterOverridesValid.value &&
    !proxyState.value.invalid,
)
function isPendingRestore(key: GroupTimeoutKey | GroupPolicyCountKey): boolean {
  return draft.value?.overrides[key] === undefined && saved.value?.overrides[key] !== undefined
}
const headerRulesOverridden = computed(() => draft.value?.overrides.header_rules !== undefined)
const headerRulesPendingRestore = computed(
  () => !headerRulesOverridden.value && saved.value?.overrides.header_rules !== undefined,
)
const displayedHeaderRules = computed<HeaderRulesDto>(() => {
  if (draft.value?.overrides.header_rules !== undefined) return draft.value.overrides.header_rules
  if (headerRulesPendingRestore.value) return { set: {}, remove: [] }
  return saved.value?.effective.header_rules ?? { set: {}, remove: [] }
})
const affinityOverridden = computed(() => draft.value?.overrides.affinity_enabled !== undefined)
const affinityPendingRestore = computed(
  () => !affinityOverridden.value && saved.value?.overrides.affinity_enabled !== undefined,
)
const affinityEnabledLabel = computed(() =>
  saved.value?.effective.affinity_enabled
    ? t('group.settings.runtime.enabledValue')
    : t('group.settings.runtime.disabledValue'),
)
function resetSavedDraft(settings: GroupSettingsDto): void {
  saved.value = settings
  draft.value = createGroupSettingsDraft(settings)
  headerRulesValid.value = true
  headerRulesInvalidEdits.value = false
  headerRulesEditorRevision.value += 1
  parameterOverridesValid.value = true
  parameterOverridesInvalidEdits.value = false
  parameterOverridesEditorRevision.value += 1
  proxyMode.value = settings.proxy.configured_mode
  proxyEndpoint.value = ''
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
    value === 'parameters' ||
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

function setPolicyCountOverride(key: GroupPolicyCountKey, enabled: boolean): void {
  if (!draft.value || !saved.value) return
  draft.value = setGroupPolicyCountOverride(draft.value, key, enabled, saved.value.effective[key])
}

function setPolicyCountValue(key: GroupPolicyCountKey, value: string): void {
  if (!draft.value) return
  draft.value = {
    ...draft.value,
    overrides: { ...draft.value.overrides, [key]: Number(value) },
  }
}

function policyCountError(key: GroupPolicyCountKey): string | undefined {
  const value = draft.value?.overrides[key]
  return value !== undefined && (!Number.isSafeInteger(value) || value < 0)
    ? t('group.settings.runtime.nonNegativeIntegerError')
    : undefined
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

async function toggleHeaderRulesOverride(): Promise<void> {
  if (!draft.value || !saved.value) return
  const overrides = { ...draft.value.overrides }
  if (headerRulesOverridden.value) {
    delete overrides.header_rules
  } else {
    overrides.header_rules = {
      set: { ...saved.value.effective.header_rules.set },
      remove: [...saved.value.effective.header_rules.remove],
    }
  }
  draft.value = { ...draft.value, overrides }
  headerRulesValid.value = true
  headerRulesInvalidEdits.value = false
  await nextTick()
  headerRulesEditorRevision.value += 1
}

function updateParameterOverrides(value: ParameterOverrideRuleDto[]): void {
  if (!draft.value) return
  const overrides = { ...draft.value.overrides }
  if (value.length === 0) delete overrides.parameter_overrides
  else overrides.parameter_overrides = value
  draft.value = { ...draft.value, overrides }
}

function toggleAffinityOverride(): void {
  if (!draft.value || !saved.value) return
  const overrides = { ...draft.value.overrides }
  if (affinityOverridden.value) delete overrides.affinity_enabled
  else overrides.affinity_enabled = saved.value.effective.affinity_enabled
  draft.value = { ...draft.value, overrides }
}

function setAffinityValue(value: boolean): void {
  if (!draft.value) return
  draft.value = {
    ...draft.value,
    overrides: { ...draft.value.overrides, affinity_enabled: value },
  }
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
    const body = {
      ...patch.value,
      ...(proxyState.value.dirty && proxyState.value.value !== undefined
        ? { proxy: proxyState.value.value }
        : {}),
    }
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
            :connection-type="draft.connection_type"
            :default-base-url="selectedChannel?.default_base_url ?? ''"
            :default-base-urls="selectedChannel?.default_base_urls ?? []"
            :param-fields="channelParamFields"
            :params="draft.params"
            :name="draft.name"
            :validation-model="draft.validation_model"
            :models="modelsQuery.data.value?.items ?? []"
            :weight-manual="draft.weight_manual"
            :price-multiplier="draft.price_multiplier"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :params-disabled="channelParamsDisabled"
            :name-error="nameError"
            :param-errors="paramErrors"
            @update:param="updateParam"
            @update:name="draft.name = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @update:price-multiplier="draft.price_multiplier = $event"
            @update:enabled="draft.enabled = $event"
          />
          <GroupSettingsBaseForm
            section="routing"
            :channel-id="draft.channel_id"
            :connection-type="draft.connection_type"
            :default-base-url="selectedChannel?.default_base_url ?? ''"
            :default-base-urls="selectedChannel?.default_base_urls ?? []"
            :param-fields="channelParamFields"
            :params="draft.params"
            :name="draft.name"
            :validation-model="draft.validation_model"
            :models="modelsQuery.data.value?.items ?? []"
            :weight-manual="draft.weight_manual"
            :price-multiplier="draft.price_multiplier"
            :enabled="draft.enabled"
            :pending="mutationPending"
            :params-disabled="channelParamsDisabled"
            :name-error="nameError"
            :param-errors="paramErrors"
            @update:param="updateParam"
            @update:name="draft.name = $event"
            @update:validation-model="draft.validation_model = $event"
            @update:weight-manual="draft.weight_manual = $event"
            @update:price-multiplier="draft.price_multiplier = $event"
            @update:enabled="draft.enabled = $event"
          />
          <section id="settings-runtime" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.runtime') }}</h3>
              <p>{{ t('group.settings.runtime.description') }}</p>
            </header>
            <div class="group-settings__runtime">
              <SettingRow
                :label="t('common.proxy.title')"
                :value="proxyValue"
                :help="proxySupported ? undefined : t('common.proxy.unsupportedHelp')"
                :source-label="
                  !proxySupported
                    ? t('common.proxy.unsupportedBadge')
                    : proxyOverridden
                      ? t('group.settings.runtime.override')
                      : proxyPendingRestore
                        ? t('group.settings.runtime.pendingRestoreSource')
                        : t('group.settings.runtime.inherited')
                "
                :action-label="
                  proxyOverridden
                    ? t('group.settings.runtime.useInherited')
                    : t('group.settings.runtime.useOverride')
                "
                :overridden="proxySupported && proxyOverridden"
                :pending-restore="proxySupported && proxyPendingRestore"
                :locked="!proxySupported"
                :disabled="mutationPending || selectedChannel === undefined || !proxySupported"
                @toggle="toggleProxyOverride"
              >
                <template #control>
                  <ProxyOverrideControl
                    :base="saved.proxy"
                    :mode="proxyMode"
                    :endpoint="proxyEndpoint"
                    :disabled="mutationPending"
                    @update:mode="proxyMode = $event"
                    @update:endpoint="proxyEndpoint = $event"
                  />
                </template>
              </SettingRow>
              <SettingRow
                v-for="key in timeoutKeys"
                :key="key"
                :label="t(`group.settings.runtime.${key}`)"
                :value="
                  isPendingRestore(key)
                    ? t('group.settings.runtime.resetPending')
                    : t('group.settings.runtime.effective', { value: saved.effective[key] })
                "
                :source-label="
                  draft.overrides[key] !== undefined
                    ? t('group.settings.runtime.override')
                    : isPendingRestore(key)
                      ? t('group.settings.runtime.pendingRestoreSource')
                      : t('group.settings.runtime.inherited')
                "
                :action-label="
                  draft.overrides[key] === undefined
                    ? t('group.settings.runtime.useOverride')
                    : t('group.settings.runtime.useInherited')
                "
                :overridden="draft.overrides[key] !== undefined"
                :pending-restore="isPendingRestore(key)"
                :disabled="mutationPending"
                @toggle="setTimeoutOverride(key, draft.overrides[key] === undefined)"
              >
                <template #control>
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
              </SettingRow>
              <SettingRow
                v-for="policy in policyRows"
                :key="policy.key"
                :label="t(`group.settings.runtime.${policy.key}`)"
                :value="
                  isPendingRestore(policy.key)
                    ? t('group.settings.runtime.resetPending')
                    : t('group.settings.runtime.effectiveCount', {
                        value: saved.effective[policy.key],
                      })
                "
                :help="t(`group.settings.runtime.${policy.helpKey}`)"
                :source-label="
                  draft.overrides[policy.key] !== undefined
                    ? t('group.settings.runtime.override')
                    : isPendingRestore(policy.key)
                      ? t('group.settings.runtime.pendingRestoreSource')
                      : t('group.settings.runtime.inherited')
                "
                :action-label="
                  draft.overrides[policy.key] === undefined
                    ? t('group.settings.runtime.useOverride')
                    : t('group.settings.runtime.useInherited')
                "
                :overridden="draft.overrides[policy.key] !== undefined"
                :pending-restore="isPendingRestore(policy.key)"
                :disabled="mutationPending"
                @toggle="
                  setPolicyCountOverride(policy.key, draft.overrides[policy.key] === undefined)
                "
              >
                <template #control>
                  <div class="group-settings__runtime-input">
                    <CompactFieldError
                      :id="`group-settings-${policy.key}`"
                      :error="policyCountError(policy.key)"
                    >
                      <template #default="{ invalid, describedBy }">
                        <AppTextInput
                          :id="`group-settings-${policy.key}`"
                          type="number"
                          min="0"
                          step="1"
                          inputmode="numeric"
                          :model-value="String(draft.overrides[policy.key])"
                          :label="
                            t('group.settings.runtime.valueFor', {
                              field: t(`group.settings.runtime.${policy.key}`),
                            })
                          "
                          appearance="surface"
                          size="compact"
                          monospace
                          :disabled="mutationPending"
                          :invalid="invalid"
                          :described-by="describedBy"
                          @update:model-value="setPolicyCountValue(policy.key, $event)"
                        />
                      </template>
                    </CompactFieldError>
                    <span aria-hidden="true">{{ t('group.settings.runtime.countUnit') }}</span>
                  </div>
                </template>
              </SettingRow>
              <SettingRow
                :label="t('group.settings.runtime.affinity_enabled')"
                :value="
                  affinityPendingRestore
                    ? t('group.settings.runtime.resetPending')
                    : affinityEnabledLabel
                "
                :help="t('group.settings.runtime.affinityHelp')"
                :source-label="
                  affinityOverridden
                    ? t('group.settings.runtime.override')
                    : affinityPendingRestore
                      ? t('group.settings.runtime.pendingRestoreSource')
                      : t('group.settings.runtime.inherited')
                "
                :action-label="
                  affinityOverridden
                    ? t('group.settings.runtime.useInherited')
                    : t('group.settings.runtime.useOverride')
                "
                :overridden="affinityOverridden"
                :pending-restore="affinityPendingRestore"
                :divided="false"
                :disabled="mutationPending"
                @toggle="toggleAffinityOverride"
              >
                <template #control>
                  <AppSwitch
                    :model-value="draft.overrides.affinity_enabled ?? false"
                    :disabled="mutationPending"
                    :label="t('group.settings.runtime.affinity_enabled')"
                    @update:model-value="setAffinityValue"
                  />
                </template>
              </SettingRow>
            </div>
          </section>
          <section id="settings-parameters" class="group-settings__section">
            <header>
              <h3>{{ t('group.settings.sections.parameters') }}</h3>
              <p>{{ t('group.settings.parameterOverrides.description') }}</p>
            </header>
            <ParameterOverrideRulesEditor
              :key="parameterOverridesEditorRevision"
              :model-value="draft.overrides.parameter_overrides ?? []"
              :protocols="parameterOverrideProtocols"
              :models="modelsQuery.data.value?.items ?? []"
              :disabled="mutationPending"
              @update:valid="parameterOverridesValid = $event"
              @update:invalid-edits="parameterOverridesInvalidEdits = $event"
              @update:model-value="updateParameterOverrides"
            />
          </section>
          <section id="settings-headers" class="group-settings__section">
            <SettingBlock
              :title="t('group.settings.sections.headers')"
              :help="t('group.settings.headers.description')"
              :meta="headerSummary()"
              :source-label="
                headerRulesOverridden
                  ? t('group.settings.runtime.override')
                  : headerRulesPendingRestore
                    ? t('group.settings.runtime.pendingRestoreSource')
                    : t('group.settings.runtime.inherited')
              "
              :action-label="
                headerRulesOverridden
                  ? t('group.settings.runtime.useInherited')
                  : t('group.settings.runtime.useOverride')
              "
              :overridden="headerRulesOverridden"
              :pending-restore="headerRulesPendingRestore"
              :disabled="mutationPending"
              @toggle="toggleHeaderRulesOverride"
            >
              <HeaderRulesEditor
                :key="headerRulesEditorRevision"
                appearance="ledger"
                :model-value="displayedHeaderRules"
                :disabled="mutationPending || !headerRulesOverridden"
                :show-notice="false"
                :show-add="headerRulesOverridden"
                :remove-label="t('group.settings.runtime.headerRemove')"
                :remove-hint="t('group.settings.runtime.headerRemoveHint')"
                @update:valid="headerRulesValid = $event"
                @update:invalid-edits="headerRulesInvalidEdits = $event"
                @update:model-value="updateHeaderRules"
              />
            </SettingBlock>
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
  gap: var(--space-1);
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
}
</style>
