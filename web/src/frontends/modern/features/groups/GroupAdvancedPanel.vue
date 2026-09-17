<script setup lang="ts">
import { protocolLabel } from '@modern/i18n/protocols'
import { Plus, Trash2 } from '@lucide/vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useMessageSource } from '@modern/app/messages'
import { useI18n } from 'vue-i18n'
import {
  getGroupSettings,
  groupSettingsKey,
  runtimeNumbers,
  runtimeSwitches,
  saveGroupSettings,
  type AdvancedSettingsPatch,
  type GroupModel,
  type GroupSettings,
  type ParameterRule,
  type RuntimeNumber,
  type RuntimeSettings,
} from '@modern/api/group-detail'
import type { GroupChannel } from '@modern/api/group-create'
import type { GroupRow } from '@modern/api/groups'
import {
  AppProtocolTag,
  AppButton,
  AppCollectionState,
  AppIconButton,
  AppNotice,
  AppFormSection,
  AppSearchSelect,
  AppSegmentedField,
  AppSelect,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { validBaseURL } from './group-create-rules'
import { validProxyURL } from '@modern/app/proxy'
import GroupWorkspacePanel from './GroupWorkspacePanel.vue'
import ParameterRulesEditor from '../config/ParameterRulesEditor.vue'
import { groupValidationModelOptions } from './group-model-options'

const props = defineProps<{ group: GroupRow; channel?: GroupChannel; models: GroupModel[] }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const query = useQuery({
  queryKey: groupSettingsKey(props.group.id),
  queryFn: ({ signal }) => getGroupSettings(client, props.group.id, signal),
})
const saved = ref<GroupSettings>()
const params = ref<Record<string, string>>({})
const validationModel = ref('')
const validationProtocol = ref('')
const numbers = ref<Partial<Record<RuntimeNumber, string>>>({})
const switches = ref<Record<string, string>>({})
const proxyMode = ref('inherit')
const proxyURL = ref('')
const headersMode = ref('inherit')
const headers = ref<{ key: number; name: string; value: string }[]>([])
const removeHeaders = ref('')
const rules = ref<ParameterRule[]>([])
const rulesValid = ref(true)
const rulesEditor = ref<InstanceType<typeof ParameterRulesEditor>>()
const baseline = ref('')
const attempted = ref(false)
const saving = ref(false)
const error = ref('')
const controller = new AbortController()
let nextHeader = 0
function snapshot(): string {
  return JSON.stringify([
    params.value,
    validationModel.value,
    validationProtocol.value,
    numbers.value,
    switches.value,
    proxyMode.value,
    proxyURL.value,
    headersMode.value,
    headers.value,
    removeHeaders.value,
    rules.value,
  ])
}
const dirty = computed(
  () => Boolean(saved.value) && (snapshot() !== baseline.value || !rulesValid.value),
)
watch(
  query.data,
  (data) => {
    if (!data || dirty.value || saving.value) return
    saved.value = data
    params.value = { ...data.params }
    validationModel.value = data.validationModel ?? ''
    validationProtocol.value = data.validationProtocol ?? ''
    numbers.value = Object.fromEntries(
      runtimeNumbers.map((key) => [
        key,
        data.overrides[key] === undefined ? '' : String(data.overrides[key]),
      ]),
    )
    switches.value = Object.fromEntries(
      runtimeSwitches.map((key) => [
        key,
        data.overrides[key] === undefined ? '' : String(data.overrides[key]),
      ]),
    )
    proxyMode.value = data.proxy.mode
    // display_url 可能脱敏；未编辑时不能把它作为代理凭据重新写回。
    proxyURL.value = ''
    headersMode.value = data.overrides.header_rules === undefined ? 'inherit' : 'custom'
    const value = data.overrides.header_rules ?? data.effective.header_rules
    headers.value = Object.entries(value.set).map(([name, value]) => ({
      key: nextHeader++,
      name,
      value,
    }))
    removeHeaders.value = value.remove.join('\n')
    rules.value = JSON.parse(JSON.stringify(data.overrides.parameter_overrides ?? []))
    rulesValid.value = true
    baseline.value = snapshot()
  },
  { immediate: true },
)
const inheritOptions = computed(() => [
  { value: 'inherit', label: t('groupDetail.inherit') },
  { value: 'custom', label: t('groupDetail.override') },
])
const switchOptions = computed(() => [
  { value: '', label: t('groupDetail.inherit') },
  { value: 'true', label: t('groupDetail.on') },
  { value: 'false', label: t('groupDetail.off') },
])
const proxyOptions = computed(() =>
  ['inherit', 'direct', 'custom'].map((value) => ({
    value,
    label: t('groupCreate.proxy' + value[0]!.toUpperCase() + value.slice(1)),
  })),
)
const modelOptions = computed(() => [
  { value: '', label: t('groupDetail.automatic') },
  ...groupValidationModelOptions(props.models),
])
const protocolOptions = computed(() =>
  (saved.value?.validationProtocols ?? []).map((value) => ({
    value,
    label: protocolLabel(value, t),
  })),
)
const paramErrors = computed(() =>
  Object.fromEntries(
    (props.channel?.fields ?? []).flatMap((field) => {
      const value = (params.value[field.key] ?? '').trim()
      const invalid =
        (field.required && !value) ||
        (value &&
          field.inputKind === 'url' &&
          (!validBaseURL(value) ||
            (props.group.connectionType === 'subscription' && !value.startsWith('https://'))))
      return invalid ? [[field.key, t('groupDetail.invalidValue')]] : []
    }),
  ),
)
function numberInvalid(key: RuntimeNumber): boolean {
  const value = numbers.value[key] ?? ''
  return (
    Boolean(value) &&
    (!/^\d+$/u.test(value) ||
      !Number.isSafeInteger(Number(value)) ||
      Number(value) < (key === 'blacklist_threshold' ? 0 : 1))
  )
}
const proxyChanged = computed(
  () => proxyMode.value !== saved.value?.proxy.mode || Boolean(proxyURL.value),
)
const proxyInvalid = computed(
  () => proxyChanged.value && proxyMode.value === 'custom' && !validProxyURL(proxyURL.value.trim()),
)
const headerInvalid = computed(() => {
  if (headersMode.value !== 'custom') return false
  const names = headers.value.map((header) => header.name.trim().toLowerCase())
  return (
    new Set(names).size !== names.length ||
    headers.value.some(
      (header) =>
        !/^[!#$%&'*+.^_`|~\w-]+$/u.test(header.name.trim()) || /[\r\n]/u.test(header.value),
    )
  )
})
async function save(): Promise<void> {
  if (!saved.value || !dirty.value || saving.value) return
  attempted.value = true
  if (!rulesValid.value) {
    await rulesEditor.value?.focusFirstInvalid()
    return
  }
  if (
    Object.keys(paramErrors.value).length ||
    runtimeNumbers.some(numberInvalid) ||
    proxyInvalid.value ||
    headerInvalid.value
  )
    return
  const base = saved.value
  const overrides: RuntimeSettings = JSON.parse(JSON.stringify(base.overrides))
  for (const key of runtimeNumbers) {
    if (numbers.value[key]) overrides[key] = Number(numbers.value[key])
    else delete overrides[key]
  }
  for (const key of runtimeSwitches) {
    if (switches.value[key]) overrides[key] = switches.value[key] === 'true'
    else delete overrides[key]
  }
  if (headersMode.value === 'inherit') delete overrides.header_rules
  else
    overrides.header_rules = {
      set: Object.fromEntries(headers.value.map((header) => [header.name.trim(), header.value])),
      remove: removeHeaders.value
        .split(/\r?\n/u)
        .map((value) => value.trim())
        .filter(Boolean),
    }
  if (rules.value.length) overrides.parameter_overrides = rules.value
  else delete overrides.parameter_overrides
  const patch: AdvancedSettingsPatch = {}
  const nextParams = Object.fromEntries(
    Object.entries(params.value).map(([key, value]) => [key, value.trim()]),
  )
  if (JSON.stringify(nextParams) !== JSON.stringify(base.params)) patch.params = nextParams
  if ((validationModel.value || null) !== base.validationModel)
    patch.validation_model = validationModel.value || null
  if (validationProtocol.value && validationProtocol.value !== base.validationProtocol)
    patch.validation_protocol = validationProtocol.value
  if (JSON.stringify(overrides) !== JSON.stringify(base.overrides)) patch.overrides = overrides
  if (proxyChanged.value)
    patch.proxy =
      proxyMode.value === 'inherit'
        ? null
        : proxyMode.value === 'direct'
          ? { mode: 'direct' }
          : { mode: 'custom', url: proxyURL.value.trim() }
  if (!Object.keys(patch).length) {
    emit('close')
    return
  }
  saving.value = true
  error.value = ''
  try {
    await cache.cancelQueries({ queryKey: groupSettingsKey(props.group.id) })
    const result = await saveGroupSettings(client, props.group.id, patch, controller.signal)
    if (controller.signal.aborted) return
    baseline.value = snapshot()
    cache.setQueryData(groupSettingsKey(props.group.id), result)
    emit('saved')
    emit('close')
  } catch {
    if (!controller.signal.aborted) error.value = t('groups.edit.saveFailed')
  } finally {
    saving.value = false
  }
}
onScopeDispose(() => controller.abort())
useMessageSource(() => (error.value ? { text: error.value, tone: 'danger' } : undefined))
</script>

<template>
  <GroupWorkspacePanel
    :title="t('groupDetail.advanced')"
    :description="group.name"
    :dirty="dirty"
    :pending="saving"
    :loading="query.isFetching.value"
    :save-disabled="!saved"
    @close="emit('close')"
    @save="save"
  >
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState v-else-if="!saved" :title="t('groups.edit.loadFailed')" error
      ><AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton></AppCollectionState
    >
    <template v-else>
      <AppFormSection :title="t('groupDetail.connection')">
        <AppTextField
          v-for="field in channel?.fields ?? []"
          :key="field.key"
          :model-value="params[field.key] ?? ''"
          :label="field.label"
          :placeholder="field.defaultValue"
          :type="field.sensitive ? 'password' : 'text'"
          size="sm"
          :disabled="saving"
          :error="attempted ? paramErrors[field.key] : undefined"
          autocomplete="off"
          @update:model-value="params[field.key] = $event"
        />
        <div v-if="group.connectionType === 'api_key'" class="modern-advanced-columns">
          <AppSearchSelect
            v-model="validationModel"
            :label="t('groupDetail.validationModel')"
            :options="modelOptions"
            allow-custom
            size="sm"
            :disabled="saving"
          />
          <AppSelect
            v-if="protocolOptions.length"
            v-model="validationProtocol"
            :label="t('groupDetail.validationProtocol')"
            :options="protocolOptions"
            size="sm"
            :disabled="saving || protocolOptions.length === 1"
          >
            <template #value="{ value, label }"
              ><AppProtocolTag v-if="value" :protocol="value" /><span v-else>{{
                label
              }}</span></template
            >
            <template #option="{ option }"
              ><AppProtocolTag v-if="option.value" :protocol="option.value" /><span v-else>{{
                option.label
              }}</span></template
            >
          </AppSelect>
        </div>
        <template v-if="channel?.proxy">
          <div class="modern-advanced-columns">
            <AppSegmentedField
              v-model="proxyMode"
              :label="t('groupCreate.proxy')"
              :options="proxyOptions"
              size="sm"
              :disabled="saving"
            />
            <AppTextField
              v-if="proxyMode === 'custom'"
              v-model="proxyURL"
              :label="t('groupCreate.proxyURL')"
              :placeholder="
                saved.proxy.mode === 'custom' ? saved.proxy.display : 'http://127.0.0.1:7890'
              "
              :description="
                saved.proxy.mode === 'custom' ? t('groupDetail.proxyUnchanged') : undefined
              "
              :error="attempted && proxyInvalid ? t('groupCreate.proxyError') : undefined"
              size="sm"
              :disabled="saving"
              autocomplete="off"
            />
          </div>
        </template>
      </AppFormSection>
      <AppFormSection :title="t('groupDetail.runtime')" :description="t('groupDetail.runtimeHelp')">
        <div class="modern-advanced-columns">
          <AppTextField
            v-for="key in runtimeNumbers"
            :key="key"
            :model-value="numbers[key] ?? ''"
            :label="t('groupDetail.runtimeFields.' + key)"
            :placeholder="String(saved.effective[key])"
            :description="t('groupDetail.effective', { value: saved.effective[key] })"
            :error="attempted && numberInvalid(key) ? t('groupDetail.invalidNumber') : undefined"
            inputmode="numeric"
            size="sm"
            :disabled="saving"
            @update:model-value="numbers[key] = $event"
          />
          <AppSegmentedField
            v-for="key in runtimeSwitches"
            :key="key"
            :model-value="switches[key] ?? ''"
            :label="t('groupDetail.runtimeFields.' + key)"
            :options="switchOptions"
            :description="
              t('groupDetail.effective', {
                value: t(saved.effective[key] ? 'groupDetail.on' : 'groupDetail.off'),
              })
            "
            size="sm"
            :disabled="saving"
            @update:model-value="switches[key] = $event"
          />
        </div>
      </AppFormSection>
      <AppFormSection compact :title="t('groupDetail.headers')">
        <template #actions
          ><AppSegmentedField
            v-model="headersMode"
            class="modern-advanced-mode"
            :label="t('groupDetail.headers')"
            label-hidden
            :options="inheritOptions"
            size="xs"
            :disabled="saving" /><AppIconButton
            :icon="Plus"
            :label="t('groupDetail.addHeader')"
            size="xs"
            :disabled="saving || headersMode === 'inherit'"
            @click="headers.push({ key: nextHeader++, name: '', value: '' })"
        /></template>
        <div v-if="headers.length" class="modern-advanced-header-labels" aria-hidden="true">
          <span>{{ t('groupDetail.headerName') }}</span
          ><span>{{ t('groupDetail.headerValue') }}</span>
        </div>
        <div v-for="header in headers" :key="header.key" class="modern-advanced-header-row">
          <AppTextField
            v-model="header.name"
            :label="t('groupDetail.headerName')"
            label-hidden
            :placeholder="t('groupDetail.headerName')"
            size="xs"
            :disabled="saving || headersMode === 'inherit'"
          />
          <AppTextField
            v-model="header.value"
            :label="t('groupDetail.headerValue')"
            label-hidden
            :placeholder="t('groupDetail.headerValue')"
            size="xs"
            :disabled="saving || headersMode === 'inherit'"
          />
          <AppIconButton
            :icon="Trash2"
            :label="t('groupDetail.removeHeader')"
            size="xs"
            :disabled="saving || headersMode === 'inherit'"
            @click="headers = headers.filter((row) => row.key !== header.key)"
          />
        </div>
        <AppTextArea
          v-model="removeHeaders"
          :label="t('groupDetail.removeHeaders')"
          :rows="2"
          size="xs"
          :placeholder="t('groupDetail.oneHeaderPerLine')"
          mono
          :disabled="saving || headersMode === 'inherit'"
        />
        <AppNotice v-if="attempted && headerInvalid" tone="danger">{{
          t('groupDetail.invalidHeaders')
        }}</AppNotice>
      </AppFormSection>
      <AppFormSection compact :title="t('groupDetail.parameters')">
        <ParameterRulesEditor
          ref="rulesEditor"
          v-model="rules"
          :protocols="channel?.parameterProtocols ?? []"
          :models="models"
          :disabled="saving"
          :attempted="attempted"
          @update:valid="rulesValid = $event"
        />
      </AppFormSection>
    </template>
  </GroupWorkspacePanel>
</template>

<style scoped>
.modern-advanced-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  align-items: start;
  gap: var(--modern-space-3) var(--modern-space-4);
}
.modern-advanced-mode {
  width: var(--modern-menu-min-width);
}
.modern-advanced-header-labels,
.modern-advanced-header-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.5fr) var(--modern-control-xs);
  align-items: start;
  gap: var(--modern-space-2);
}
.modern-advanced-header-labels {
  margin-bottom: calc(-1 * var(--modern-space-1));
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
@container modern-workspace-panel (max-width: 440px) {
  .modern-advanced-columns {
    grid-template-columns: 1fr;
  }
}
@media (max-width: 760px) {
  .modern-advanced-header-labels,
  .modern-advanced-header-row {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.5fr) var(--modern-touch-target);
  }
}
</style>
