<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronDown, ChevronRight, Plus, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { createUUID } from '@shared/uuid'
import type { AuditConfig, AuditAccessKey, AuditRule } from '@modern/api/experimental'
import {
  AppBadge,
  AppButton,
  AppIconButton,
  AppMultiSelect,
  AppNotice,
  AppSelect,
  AppSwitch,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'

const props = defineProps<{
  modelValue: AuditConfig
  accessKeys: AuditAccessKey[]
  preset?: AuditConfig
  disabled?: boolean
  error?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: AuditConfig] }>()
const { t } = useI18n()
const expanded = ref<string[]>([])
const scope = computed(() => {
  const options = props.accessKeys.map((k) => ({ value: String(k.id), label: k.name }))
  for (const id of props.modelValue.access_key_ids)
    if (!options.some((o) => o.value === String(id)))
      options.push({ value: String(id), label: t('requestAudit.deletedKey') })
  return options
})
const actions = computed(() =>
  ['block', 'warn'].map((value) => ({ value, label: t('requestAudit.actions.' + value) })),
)
const missingPresetRules = computed(() =>
  (props.preset?.rules ?? []).filter(
    (preset) => !props.modelValue.rules.some((rule) => rule.id === preset.id),
  ),
)
function update(change: (value: AuditConfig) => void) {
  if (props.disabled) return
  const draft = JSON.parse(JSON.stringify(props.modelValue)) as AuditConfig
  change(draft)
  emit('update:modelValue', draft)
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
  update((value) =>
    value.rules.push({
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
  if (props.modelValue.rules.length + missingPresetRules.value.length > 16) return
  update((value) => value.rules.push(...missingPresetRules.value.map((rule) => ({ ...rule }))))
}
</script>

<template>
  <div class="modern-request-audit">
    <AppNotice v-if="error" tone="danger">{{ t('requestAudit.invalid') }}</AppNotice>
    <AppMultiSelect
      :model-value="modelValue.access_key_ids.map(String)"
      :options="scope"
      :label="t('requestAudit.scope')"
      :description="t('requestAudit.scopeHelp')"
      :disabled="disabled"
      @update:model-value="update((v) => (v.access_key_ids = $event.map(Number)))"
    />
    <div class="modern-request-audit-heading modern-request-audit-toolbar">
      <span>{{ t('requestAudit.rulesTitle') }} · {{ modelValue.rules.length }}/16</span>
      <AppButton
        size="sm"
        :disabled="
          disabled ||
          !missingPresetRules.length ||
          modelValue.rules.length + missingPresetRules.length > 16
        "
        @click="addPreset"
        >{{ t('requestAudit.addPreset') }}</AppButton
      >
      <AppButton
        :icon="Plus"
        size="sm"
        :disabled="disabled || modelValue.rules.length >= 16"
        @click="addRule"
        >{{ t('requestAudit.addRule') }}</AppButton
      >
    </div>
    <p class="modern-request-audit-help">{{ t('requestAudit.rulesHelp') }}</p>
    <div v-for="(rule, index) in modelValue.rules" :key="rule.id" class="modern-request-audit-rule">
      <div class="modern-request-audit-heading">
        <AppButton
          variant="text"
          size="sm"
          :icon="isOpen(rule) ? ChevronDown : ChevronRight"
          :aria-expanded="isOpen(rule)"
          :aria-controls="'guardrail-' + rule.id"
          class="modern-request-audit-name"
          @click="toggle(rule.id)"
          >{{ rule.name || t('requestAudit.unnamed') }}</AppButton
        >
        <AppBadge
          :tone="rule.action === 'block' ? 'danger' : 'warning'"
          size="xs"
          variant="plain"
          >{{ t('requestAudit.actions.' + rule.action) }}</AppBadge
        >
        <AppSwitch
          :model-value="rule.enabled"
          :label="t('requestAudit.ruleEnabled')"
          :disabled="disabled"
          @update:model-value="update((v) => (v.rules[index]!.enabled = $event))"
        />
        <AppIconButton
          :icon="Trash2"
          size="xs"
          :label="t('requestAudit.removeRule')"
          :disabled="disabled"
          @click="update((v) => v.rules.splice(index, 1))"
        />
      </div>
      <div v-if="isOpen(rule)" :id="'guardrail-' + rule.id" class="modern-request-audit-fields">
        <div class="modern-request-audit-grid">
          <AppTextField
            :model-value="rule.name"
            :label="t('requestAudit.ruleName')"
            :disabled="disabled"
            @update:model-value="update((v) => (v.rules[index]!.name = $event))"
          />
          <AppSelect
            :model-value="rule.action"
            :options="actions"
            :label="t('requestAudit.action')"
            :disabled="disabled"
            @update:model-value="
              update((v) => (v.rules[index]!.action = $event as AuditRule['action']))
            "
          />
          <AppTextField
            :model-value="String(rule.threshold)"
            type="number"
            min="0.01"
            max="1"
            step="0.05"
            :label="t('requestAudit.threshold')"
            :disabled="disabled"
            @update:model-value="update((v) => (v.rules[index]!.threshold = Number($event)))"
          />
        </div>
        <AppTextArea
          :model-value="rule.instructions"
          :rows="2"
          :label="t('requestAudit.instructions')"
          :disabled="disabled"
          @update:model-value="update((v) => (v.rules[index]!.instructions = $event))"
        />
      </div>
    </div>
    <p class="modern-request-audit-help">{{ t('requestAudit.coverageHelp') }}</p>
  </div>
</template>

<style scoped>
.modern-request-audit,
.modern-request-audit-fields {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.modern-request-audit-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-request-audit-heading > :first-child {
  flex: 1;
  min-width: 0;
}
.modern-request-audit-toolbar {
  flex-wrap: wrap;
}
.modern-request-audit-name {
  justify-content: flex-start;
  white-space: normal;
  overflow-wrap: anywhere;
  text-align: left;
}
.modern-request-audit-heading > span:first-child {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-request-audit-rule {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-2);
}
.modern-request-audit-grid {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr);
  gap: var(--modern-space-3);
}
.modern-request-audit-help {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
@container modern-settings-content (max-width: 620px) {
  .modern-request-audit-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
