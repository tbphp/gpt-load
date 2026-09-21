<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronDown, ChevronRight, Plus, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { SettingsResource } from '@/app/resources/settings'
import {
  validAudit,
  validJev,
  type AuditConfig,
  type AuditRule,
  type JevConfig,
} from '@/app/resources/experimental'
import SettingRow from '@/components/config/SettingRow.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppCombobox from '@/components/ui/AppCombobox.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import IconButton from '@/components/ui/IconButton.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
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
const expanded = ref<string[]>([])
const jev = computed(() => props.draft.values.jev)
const audit = computed(() => props.draft.values.request_audit)
const overridden = computed(() => props.draft.overrides.has(props.kind))
const controlsDisabled = computed(
  () => props.disabled || props.draft.readOnly.has(props.kind) || !overridden.value,
)
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
const actions = computed(() =>
  ['block', 'warn'].map((value) => ({ value, label: t('requestAudit.actions.' + value) })),
)
const missingPresetRules = computed(() =>
  props.base.settings.request_audit_preset.rules.filter(
    (preset) => !audit.value.rules.some((rule) => rule.id === preset.id),
  ),
)
const invalid = computed(() =>
  props.kind === 'jev'
    ? !validJev(jev.value) ||
      ((audit.value.enabled || (props.draft.values.auto_model?.enabled ?? false)) &&
        !jev.value.model)
    : !validAudit(audit.value) || (audit.value.enabled && !jev.value.group_id),
)
watch(invalid, (value) => emit('invalid', value), { immediate: true })
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
  update((d) => {
    if (value.enabled === false && !validAudit(audit.value))
      d.values.request_audit = JSON.parse(
        JSON.stringify(props.base.settings.values.request_audit),
      ) as AuditConfig
    Object.assign(d.values.request_audit, value)
  })
}
function editRule(index: number, value: Partial<AuditRule>) {
  update((d) => Object.assign(d.values.request_audit.rules[index]!, value))
}
function toggleOverride() {
  const draft = setSettingsOverride(props.base.settings, props.draft, props.kind, !overridden.value)
  emit('change', { key: props.kind, draft })
}
function isOpen(rule: AuditRule) {
  return expanded.value.includes(rule.id) || !rule.name.trim() || !rule.instructions.trim()
}
function toggle(id: string) {
  expanded.value = expanded.value.includes(id)
    ? expanded.value.filter((v) => v !== id)
    : [...expanded.value, id]
}
function addRule() {
  const id = 'rule_' + crypto.randomUUID().replaceAll('-', '')
  expanded.value.push(id)
  update((d) =>
    d.values.request_audit.rules.push({
      id,
      name: '',
      enabled: true,
      instructions: '',
      action: 'block',
      threshold: 0.8,
    }),
  )
}
function addPreset() {
  if (audit.value.rules.length + missingPresetRules.value.length > 16) return
  update((d) =>
    d.values.request_audit.rules.push(...missingPresetRules.value.map((rule) => ({ ...rule }))),
  )
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
      :locked="draft.readOnly.has(kind)"
      :disabled="disabled"
      :divided="false"
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
    <div v-if="kind === 'jev'" class="experimental-settings-grid">
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
          min="1"
          max="60"
          :label="t('jev.timeout')"
          :disabled="controlsDisabled"
          @update:model-value="setJev({ timeout_seconds: Number($event) })"
      /></FormField>
    </div>
    <template v-else-if="audit.enabled">
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
      <div class="experimental-settings-heading experimental-settings-toolbar">
        <strong>{{ t('requestAudit.rulesTitle') }} · {{ audit.rules.length }}/16</strong
        ><AppButton
          size="compact"
          variant="secondary"
          :disabled="
            controlsDisabled ||
            !missingPresetRules.length ||
            audit.rules.length + missingPresetRules.length > 16
          "
          @click="addPreset"
          >{{ t('requestAudit.addPreset') }}</AppButton
        ><AppButton
          size="compact"
          variant="secondary"
          :disabled="controlsDisabled || audit.rules.length >= 16"
          @click="addRule"
          ><Plus :size="14" />{{ t('requestAudit.addRule') }}</AppButton
        >
      </div>
      <p>{{ t('requestAudit.rulesHelp') }}</p>
      <div v-for="(rule, index) in audit.rules" :key="rule.id" class="experimental-settings-rule">
        <div class="experimental-settings-heading">
          <AppButton
            size="sm"
            variant="ghost"
            class="experimental-settings-name"
            :aria-expanded="isOpen(rule)"
            :aria-controls="'guardrail-' + rule.id"
            @click="toggle(rule.id)"
            ><ChevronDown v-if="isOpen(rule)" :size="14" /><ChevronRight v-else :size="14" />{{
              rule.name || t('requestAudit.unnamed')
            }}</AppButton
          >
          <StatusBadge size="compact" :tone="rule.action === 'block' ? 'danger' : 'warning'">{{
            t('requestAudit.actions.' + rule.action)
          }}</StatusBadge>
          <AppSwitch
            :model-value="rule.enabled"
            :label="t('requestAudit.ruleEnabled')"
            :disabled="controlsDisabled"
            @update:model-value="editRule(index, { enabled: $event })"
          />
          <IconButton
            size="xs"
            variant="ghost"
            :label="t('requestAudit.removeRule')"
            :disabled="controlsDisabled"
            @click="update((d) => d.values.request_audit.rules.splice(index, 1))"
            ><Trash2 :size="14"
          /></IconButton>
        </div>
        <div
          v-if="isOpen(rule)"
          :id="'guardrail-' + rule.id"
          class="experimental-settings-rule-body"
        >
          <div class="experimental-settings-grid">
            <FormField :id="rule.id + '-name'" :label="t('requestAudit.ruleName')"
              ><AppTextInput
                :id="rule.id + '-name'"
                :model-value="rule.name"
                :label="t('requestAudit.ruleName')"
                :disabled="controlsDisabled"
                @update:model-value="editRule(index, { name: $event })"
            /></FormField>
            <FormField :id="rule.id + '-action'" :label="t('requestAudit.action')"
              ><AppSelect
                :id="rule.id + '-action'"
                :model-value="rule.action"
                :label="t('requestAudit.action')"
                :options="actions"
                :disabled="controlsDisabled"
                @update:model-value="editRule(index, { action: $event as AuditRule['action'] })"
            /></FormField>
            <FormField :id="rule.id + '-threshold'" :label="t('requestAudit.threshold')"
              ><AppTextInput
                :id="rule.id + '-threshold'"
                :model-value="String(rule.threshold)"
                type="number"
                min="0.01"
                max="1"
                step="0.05"
                :label="t('requestAudit.threshold')"
                :disabled="controlsDisabled"
                @update:model-value="editRule(index, { threshold: Number($event) })"
            /></FormField>
          </div>
          <FormField :id="rule.id + '-instructions'" :label="t('requestAudit.instructions')">
            <textarea
              :id="rule.id + '-instructions'"
              class="experimental-settings-textarea"
              :value="rule.instructions"
              rows="2"
              :disabled="controlsDisabled"
              @input="
                editRule(index, { instructions: ($event.target as HTMLTextAreaElement).value })
              "
            />
          </FormField>
        </div>
      </div>
      <p>{{ t('requestAudit.coverageHelp') }}</p>
    </template>
    <p v-if="invalid" class="experimental-settings-error" role="alert">
      {{ t('requestAudit.invalid') }}
    </p>
  </div>
</template>

<style scoped>
.experimental-settings-fields,
.experimental-settings-rule-body {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}
.experimental-settings-fields {
  padding-bottom: var(--space-3);
}
.experimental-settings-toolbar {
  flex-wrap: wrap;
}
.experimental-settings-fields p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  line-height: 1.5;
}
.experimental-settings-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
  gap: var(--space-3);
}
.experimental-settings-heading {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: var(--space-2);
}
.experimental-settings-heading > :first-child {
  flex: 1;
  min-width: 0;
}
.experimental-settings-heading strong {
  font-size: var(--text-sm);
}
.experimental-settings-name {
  justify-content: flex-start;
  white-space: normal;
  overflow-wrap: anywhere;
  text-align: left;
}
.experimental-settings-rule {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-2);
}
.experimental-settings-textarea {
  box-sizing: border-box;
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  color: var(--color-text);
  background: var(--color-surface);
  font: inherit;
  font-size: var(--text-sm);
  resize: vertical;
}
.experimental-settings-textarea:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.experimental-settings-fields .experimental-settings-error {
  color: var(--color-danger);
}
@media (max-width: 760px) {
  .experimental-settings-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
