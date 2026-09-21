<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SettingsResource } from '@/app/resources/settings'
import {
  validAudit,
  type AuditConfig,
  type AuditRule,
  type JevConfig,
} from '@/app/resources/experimental'
import SettingRow from '@/components/config/SettingRow.vue'
import AppCombobox from '@/components/ui/AppCombobox.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import SearchableMultiSelect from '@/components/ui/SearchableMultiSelect.vue'
import { createSettingsDraft, setSettingsOverride, type SettingsDraft } from './settings-patch'
import type { SettingsDraftChange } from './use-settings-controller'

const props = defineProps<{
  kind: 'jev' | 'request_audit'
  base: SettingsResource
  draft: SettingsDraft
  disabled: boolean
  revision: number
}>()
const emit = defineEmits<{ change: [value: SettingsDraftChange]; invalid: [value: boolean] }>()
const { t } = useI18n()
const jev = computed(() => props.draft.values.jev)
const audit = computed(() => props.draft.values.request_audit)
const overridden = computed(() => props.draft.overrides.has(props.kind))
const controlsDisabled = computed(() => props.disabled || !overridden.value)
const pendingRestore = computed(
  () => !overridden.value && props.base.settings.overrides.includes(props.kind),
)
const groups = computed(() => {
  const options = props.base.settings.decision_routes.map((r) => ({
    value: String(r.group_id),
    label: r.group_name,
  }))
  if (jev.value.group_id && !options.some((o) => o.value === String(jev.value.group_id)))
    options.push({ value: String(jev.value.group_id), label: t('jev.deletedGroup') })
  return [{ value: '0', label: t('jev.anyGroup') }, ...options]
})
const models = computed(() =>
  [
    ...new Set(
      props.base.settings.decision_routes
        .filter((r) => !jev.value.group_id || r.group_id === jev.value.group_id)
        .flatMap((r) => r.models),
    ),
  ].map((value) => ({ value, label: value })),
)
const scope = computed(() => {
  const options = props.base.settings.audit_access_keys.map((k) => ({ value: k.id, label: k.name }))
  for (const id of audit.value.access_key_ids)
    if (!options.some((o) => o.value === id))
      options.push({ value: id, label: t('requestAudit.deletedKey') })
  return options
})
const modes = computed(() =>
  ['observe', 'enforce'].map((value) => ({ value, label: t('requestAudit.modes.' + value) })),
)
const rules = ref(JSON.stringify(audit.value.rules, null, 2))
const rulesInvalid = ref(false)
watch(
  () => [props.base, props.revision],
  () => {
    rules.value = JSON.stringify(audit.value.rules, null, 2)
    rulesInvalid.value = false
  },
)
watch(
  () => [rulesInvalid.value, audit.value, overridden.value],
  () =>
    emit(
      'invalid',
      props.kind === 'request_audit' &&
        overridden.value &&
        (rulesInvalid.value || !validAudit(audit.value)),
    ),
  { deep: true },
)
function update(change: (draft: SettingsDraft) => void) {
  if (controlsDisabled.value) return
  const draft = createSettingsDraft({
    values: props.draft.values,
    overrides: [...props.draft.overrides],
    read_only: [...props.draft.readOnly],
  })
  change(draft)
  emit('change', { key: props.kind, draft })
}
function setJev(value: Partial<JevConfig>) {
  update((d) => Object.assign(d.values.jev, value))
}
function selectGroup(value: string) {
  const group_id = Number(value)
  const available = props.base.settings.decision_routes
    .filter((r) => !group_id || r.group_id === group_id)
    .flatMap((r) => r.models)
  setJev({
    group_id,
    model: available.includes(jev.value.model) ? jev.value.model : (available[0] ?? ''),
  })
}
function setAudit(value: Partial<AuditConfig>) {
  if (value.enabled === false) {
    rules.value = JSON.stringify(audit.value.rules, null, 2)
    rulesInvalid.value = false
  }
  update((d) => Object.assign(d.values.request_audit, value))
}
function toggleOverride() {
  const draft = setSettingsOverride(props.base.settings, props.draft, props.kind, !overridden.value)
  emit('change', { key: props.kind, draft })
}
function editRules(value: string) {
  rules.value = value
  try {
    const parsed: unknown = JSON.parse(value)
    if (!Array.isArray(parsed)) throw new Error('array required')
    const config = { ...audit.value, rules: parsed as AuditRule[] }
    if (!validAudit(config)) throw new Error('invalid rules')
    setAudit({ rules: config.rules })
    rulesInvalid.value = false
  } catch {
    rulesInvalid.value = true
  }
}
</script>

<template>
  <div class="experimental-settings-fields">
    <SettingRow
      :label="t(kind === 'jev' ? 'jev.title' : 'requestAudit.title')"
      :value="
        kind === 'jev'
          ? jev.model
          : t(audit.enabled ? 'settings.runtime.enabled' : 'settings.runtime.disabled')
      "
      :help="t(kind === 'jev' ? 'jev.help' : 'requestAudit.help')"
      :source-label="
        t(overridden ? 'settings.runtime.overrideSource' : 'settings.runtime.defaultSource')
      "
      :action-label="
        t(overridden ? 'settings.runtime.restoreDefault' : 'settings.runtime.override')
      "
      :overridden="overridden"
      :pending-restore="pendingRestore"
      :disabled="disabled"
      @toggle="toggleOverride"
    >
      <template v-if="kind === 'request_audit'" #control
        ><AppSwitch
          :model-value="audit.enabled"
          :label="t('requestAudit.enabled')"
          :disabled="controlsDisabled"
          @update:model-value="setAudit({ enabled: $event })"
      /></template>
    </SettingRow>
    <template v-if="kind === 'jev'">
      <FormField id="experimental-jev-group" :label="t('jev.group')"
        ><AppCombobox
          id="experimental-jev-group"
          :model-value="String(jev.group_id)"
          :label="t('jev.group')"
          :options="groups"
          :empty-text="t('autoModel.decisionModelEmpty')"
          :disabled="controlsDisabled"
          @update:model-value="selectGroup"
      /></FormField>
      <FormField id="experimental-jev-model" :label="t('jev.model')"
        ><AppCombobox
          id="experimental-jev-model"
          :model-value="jev.model"
          :label="t('jev.model')"
          :options="models"
          :empty-text="t('autoModel.decisionModelEmpty')"
          :disabled="controlsDisabled"
          @update:model-value="setJev({ model: $event })"
      /></FormField>
      <FormField id="experimental-jev-timeout" :label="t('jev.timeout')"
        ><AppTextInput
          id="experimental-jev-timeout"
          :model-value="String(jev.timeout_seconds)"
          type="number"
          :label="t('jev.timeout')"
          :disabled="controlsDisabled"
          @update:model-value="setJev({ timeout_seconds: Number($event) })"
      /></FormField>
    </template>
    <template v-else-if="audit.enabled">
      <FormField
        id="audit-mode"
        :label="t('requestAudit.mode')"
        :description="t('requestAudit.modeHelp')"
        ><AppSelect
          id="audit-mode"
          :model-value="audit.mode"
          :label="t('requestAudit.mode')"
          :options="modes"
          :disabled="controlsDisabled"
          @update:model-value="setAudit({ mode: $event as AuditConfig['mode'] })"
      /></FormField>
      <FormField
        id="audit-scope"
        :label="t('requestAudit.scope')"
        :description="t('requestAudit.scopeHelp')"
      >
        <SearchableMultiSelect
          id="audit-scope"
          :label="t('requestAudit.scope')"
          :options="scope"
          :model-value="audit.access_key_ids"
          :disabled="controlsDisabled"
          :search-label="t('requestAudit.scope')"
          :search-placeholder="t('requestAudit.scope')"
          :clear-search-label="t('requestAudit.clearSelection')"
          :empty-label="t('requestAudit.noKeys')"
          :loading-label="t('common.asyncLoading')"
          :selected-label="t('requestAudit.selectedKeys', { count: audit.access_key_ids.length })"
          :add-label="t('requestAudit.selectKeys')"
          :clear-label="t('requestAudit.clearSelection')"
          :remove-label="() => t('requestAudit.removeKey')"
          @update:model-value="setAudit({ access_key_ids: $event.map(Number) })"
        />
      </FormField>
      <AppSwitch
        :model-value="audit.local_secrets"
        :label="t('requestAudit.localSecrets')"
        :disabled="controlsDisabled"
        @update:model-value="setAudit({ local_secrets: $event })"
      />
      <AppSwitch
        :model-value="audit.semantic_enabled"
        :label="t('requestAudit.semantic')"
        :disabled="controlsDisabled"
        @update:model-value="setAudit({ semantic_enabled: $event })"
      />
      <template v-if="audit.semantic_enabled">
        <p>{{ t('requestAudit.remoteHelp') }}</p>
        <FormField
          id="audit-rules"
          :label="t('requestAudit.rulesTitle')"
          :description="t('requestAudit.rulesHelp')"
          :error="rulesInvalid ? t('requestAudit.invalid') : undefined"
        >
          <textarea
            id="audit-rules"
            class="experimental-settings-json"
            :value="rules"
            rows="16"
            :disabled="controlsDisabled"
            @input="editRules(($event.target as HTMLTextAreaElement).value)"
          />
        </FormField>
      </template>
      <p>{{ t('requestAudit.coverageHelp') }}</p>
    </template>
  </div>
</template>

<style scoped>
.experimental-settings-fields {
  display: grid;
  gap: var(--space-4);
}
.experimental-settings-fields p {
  margin: 0;
  color: var(--color-text-muted);
}
.experimental-settings-json {
  box-sizing: border-box;
  width: 100%;
  padding: var(--space-3);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  color: var(--color-text);
  background: var(--color-surface);
  font-family: var(--font-mono);
  resize: vertical;
}
</style>
