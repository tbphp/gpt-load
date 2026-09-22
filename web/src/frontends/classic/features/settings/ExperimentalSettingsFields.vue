<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronDown, ChevronRight, Plus, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { createUUID } from '@shared/uuid'
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
  const id = 'rule_' + createUUID().replaceAll('-', '')
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
        pendingRestore
          ? t('settings.runtime.resetPending')
          : kind === 'jev'
            ? jev.model || '—'
            : t(audit.enabled ? 'settings.runtime.enabled' : 'settings.runtime.disabled')
      "
      :help="t(kind === 'jev' ? 'jev.help' : 'requestAudit.help')"
      :source-label="
        t(
          overridden
            ? 'settings.runtime.overrideSource'
            : pendingRestore
              ? 'settings.runtime.pendingRestoreSource'
              : 'settings.runtime.defaultSource',
        )
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
      <template #control>
        <span v-if="kind === 'jev'" class="experimental-settings-value">{{
          jev.model || '—'
        }}</span>
        <AppSwitch
          v-else
          :model-value="audit.enabled"
          :label="t('requestAudit.enabled')"
          :disabled="controlsDisabled"
          @update:model-value="setAudit({ enabled: $event })"
        />
      </template>
    </SettingRow>
    <div
      v-if="kind === 'jev' && overridden"
      class="experimental-settings-grid experimental-settings-content"
    >
      <FormField id="experimental-jev-group" :label="t('jev.group')" size="compact"
        ><AppCombobox
          id="experimental-jev-group"
          :model-value="String(jev.group_id)"
          :label="t('jev.group')"
          size="sm"
          :options="groups"
          :empty-text="t('autoModel.decisionModelEmpty')"
          :disabled="controlsDisabled"
          @update:model-value="selectGroup"
      /></FormField>
      <FormField id="experimental-jev-model" :label="t('jev.model')" size="compact"
        ><AppCombobox
          id="experimental-jev-model"
          :model-value="jev.model"
          :label="t('jev.model')"
          size="sm"
          :options="models"
          :empty-text="t('autoModel.decisionModelEmpty')"
          :disabled="controlsDisabled"
          @update:model-value="setJev({ model: $event })"
      /></FormField>
      <FormField id="experimental-jev-timeout" :label="t('jev.timeout')" size="compact"
        ><AppTextInput
          id="experimental-jev-timeout"
          :model-value="String(jev.timeout_seconds)"
          type="number"
          min="1"
          max="60"
          :label="t('jev.timeout')"
          size="sm"
          :disabled="controlsDisabled"
          @update:model-value="setJev({ timeout_seconds: Number($event) })"
      /></FormField>
    </div>
    <div
      v-else-if="kind === 'request_audit' && audit.enabled"
      class="experimental-settings-content experimental-settings-audit"
    >
      <FormField
        id="audit-scope"
        :label="t('requestAudit.scope')"
        :description="t('requestAudit.scopeHelp')"
        size="compact"
      >
        <SearchableMultiSelect
          id="audit-scope"
          size="compact"
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
      <div class="experimental-settings-toolbar">
        <div class="experimental-settings-intro">
          <h3>{{ t('requestAudit.rulesTitle') }} · {{ audit.rules.length }}/16</h3>
          <p>{{ t('requestAudit.rulesHelp') }}</p>
        </div>
        <div class="experimental-settings-actions">
          <AppButton
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
      </div>
      <div class="experimental-settings-rules">
        <div v-for="(rule, index) in audit.rules" :key="rule.id" class="experimental-settings-rule">
          <div class="experimental-settings-heading">
            <AppButton
              size="compact"
              variant="ghost"
              class="experimental-settings-name"
              :aria-expanded="isOpen(rule)"
              :aria-controls="'guardrail-' + rule.id"
              @click="toggle(rule.id)"
              ><ChevronDown v-if="isOpen(rule)" :size="14" /><ChevronRight
                v-else
                :size="14"
              /><span>{{ rule.name || t('requestAudit.unnamed') }}</span></AppButton
            >
            <div class="experimental-settings-actions">
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
          </div>
          <div
            v-if="isOpen(rule)"
            :id="'guardrail-' + rule.id"
            class="experimental-settings-rule-body"
          >
            <div class="experimental-settings-grid">
              <FormField :id="rule.id + '-name'" :label="t('requestAudit.ruleName')" size="compact"
                ><AppTextInput
                  :id="rule.id + '-name'"
                  :model-value="rule.name"
                  :label="t('requestAudit.ruleName')"
                  size="sm"
                  :disabled="controlsDisabled"
                  @update:model-value="editRule(index, { name: $event })"
              /></FormField>
              <FormField :id="rule.id + '-action'" :label="t('requestAudit.action')" size="compact"
                ><AppSelect
                  :id="rule.id + '-action'"
                  :model-value="rule.action"
                  :label="t('requestAudit.action')"
                  size="sm"
                  :options="actions"
                  :disabled="controlsDisabled"
                  @update:model-value="editRule(index, { action: $event as AuditRule['action'] })"
              /></FormField>
              <FormField
                :id="rule.id + '-threshold'"
                :label="t('requestAudit.threshold')"
                size="compact"
                ><AppTextInput
                  :id="rule.id + '-threshold'"
                  :model-value="String(rule.threshold)"
                  type="number"
                  min="0.01"
                  max="1"
                  step="0.05"
                  :label="t('requestAudit.threshold')"
                  size="sm"
                  :disabled="controlsDisabled"
                  @update:model-value="editRule(index, { threshold: Number($event) })"
              /></FormField>
            </div>
            <FormField
              :id="rule.id + '-instructions'"
              :label="t('requestAudit.instructions')"
              size="compact"
            >
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
      </div>
      <p>{{ t('requestAudit.coverageHelp') }}</p>
    </div>
    <p v-if="invalid" class="experimental-settings-error" role="alert">
      {{ t('requestAudit.invalid') }}
    </p>
  </div>
</template>

<style scoped>
.experimental-settings-fields,
.experimental-settings-audit,
.experimental-settings-rule-body {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-width: 0;
  gap: var(--space-3);
}
.experimental-settings-content {
  padding-inline: var(--space-3);
}
.experimental-settings-value {
  display: block;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.experimental-settings-fields p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  line-height: 1.5;
}
.experimental-settings-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(90px, 120px);
  align-items: start;
  gap: var(--space-3);
  min-width: 0;
}
.experimental-settings-grid > * {
  min-width: 0;
}
.experimental-settings-toolbar,
.experimental-settings-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  min-width: 0;
  gap: var(--space-2);
}
.experimental-settings-toolbar {
  flex-wrap: wrap;
}
.experimental-settings-intro {
  display: grid;
  flex: 1 1 180px;
  min-width: 0;
  gap: var(--space-1);
}
.experimental-settings-intro h3 {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 650;
}
.experimental-settings-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--space-2);
}
.experimental-settings-toolbar > .experimental-settings-actions {
  flex-wrap: wrap;
}
.experimental-settings-heading > .experimental-settings-name {
  flex: 1;
  min-width: 0;
  justify-content: flex-start;
  padding-inline: var(--space-1);
  text-align: left;
  white-space: normal;
}
.experimental-settings-name span {
  min-width: 0;
  overflow-wrap: anywhere;
}
.experimental-settings-name :deep(svg) {
  flex: none;
}
.experimental-settings-rules {
  display: grid;
  min-width: 0;
}
.experimental-settings-rule {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
  border-top: 1px dashed var(--color-border-subtle);
  padding-block: var(--space-2);
}
.experimental-settings-rule-body {
  padding: var(--space-1) var(--space-1) var(--space-2);
}
.experimental-settings-fields .experimental-settings-textarea {
  box-sizing: border-box;
  width: 100%;
  min-height: 72px;
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  line-height: 1.5;
  resize: vertical;
}
.experimental-settings-fields .experimental-settings-error {
  padding-inline: var(--space-3);
  color: var(--color-danger);
}
@media (max-width: 800px) {
  .experimental-settings-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 600px) {
  .experimental-settings-heading {
    flex-wrap: wrap;
  }
  .experimental-settings-heading > .experimental-settings-name {
    flex-basis: 100%;
  }
  .experimental-settings-heading > .experimental-settings-actions {
    margin-left: auto;
  }
}
</style>
