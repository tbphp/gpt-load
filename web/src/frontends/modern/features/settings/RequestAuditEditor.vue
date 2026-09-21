<script setup lang="ts">
import { computed } from 'vue'
import { Plus, Trash2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { AuditConfig, AuditAccessKey } from '@modern/api/experimental'
import {
  AppButton,
  AppFormSection,
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
  disabled?: boolean
  error?: string
}>()
const emit = defineEmits<{ 'update:modelValue': [value: AuditConfig] }>()
const { t } = useI18n()
const scope = computed(() => {
  const options = props.accessKeys.map((k) => ({ value: String(k.id), label: k.name }))
  for (const id of props.modelValue.access_key_ids)
    if (!options.some((o) => o.value === String(id)))
      options.push({ value: String(id), label: t('requestAudit.deletedKey') })
  return options
})
const modes = computed(() =>
  ['observe', 'enforce'].map((value) => ({ value, label: t('requestAudit.modes.' + value) })),
)
function update(change: (value: AuditConfig) => void) {
  const draft = JSON.parse(JSON.stringify(props.modelValue)) as AuditConfig
  change(draft)
  emit('update:modelValue', draft)
}
function addRule() {
  update((value) =>
    value.rules.push({
      id: 'rule_' + crypto.randomUUID().replaceAll('-', ''),
      name: '',
      instructions: '',
      threshold: 0.8,
    }),
  )
}
</script>

<template>
  <div class="modern-request-audit">
    <AppNotice v-if="error" tone="danger">{{ t('requestAudit.invalid') }}</AppNotice>
    <AppSelect
      :model-value="modelValue.mode"
      :options="modes"
      :label="t('requestAudit.mode')"
      :disabled="disabled"
      @update:model-value="update((v) => (v.mode = $event as AuditConfig['mode']))"
    />
    <AppNotice>{{ t('requestAudit.modeHelp') }}</AppNotice>
    <AppMultiSelect
      :model-value="modelValue.access_key_ids.map(String)"
      :options="scope"
      :label="t('requestAudit.scope')"
      :description="t('requestAudit.scopeHelp')"
      :disabled="disabled"
      @update:model-value="update((v) => (v.access_key_ids = $event.map(Number)))"
    />
    <AppSwitch
      :model-value="modelValue.local_secrets"
      :label="t('requestAudit.localSecrets')"
      :disabled="disabled"
      @update:model-value="update((v) => (v.local_secrets = $event))"
    />
    <AppSwitch
      :model-value="modelValue.semantic_enabled"
      :label="t('requestAudit.semantic')"
      :disabled="disabled"
      @update:model-value="update((v) => (v.semantic_enabled = $event))"
    />
    <template v-if="modelValue.semantic_enabled">
      <AppNotice>{{ t('requestAudit.remoteHelp') }}</AppNotice>
      <AppFormSection
        :title="t('requestAudit.rulesTitle')"
        :description="t('requestAudit.rulesHelp')"
      >
        <template #actions
          ><AppButton
            :icon="Plus"
            :disabled="disabled || modelValue.rules.length >= 16"
            @click="addRule"
            >{{ t('requestAudit.addRule') }}</AppButton
          ></template
        >
        <div
          v-for="(rule, index) in modelValue.rules"
          :key="rule.id"
          class="modern-request-audit-rule"
        >
          <div class="modern-request-audit-rule-heading">
            <AppTextField
              :model-value="rule.name"
              :label="t('requestAudit.ruleName')"
              :disabled="disabled"
              @update:model-value="update((v) => (v.rules[index]!.name = $event))"
            />
            <AppIconButton
              :icon="Trash2"
              :label="t('requestAudit.removeRule')"
              :disabled="disabled"
              @click="update((v) => v.rules.splice(index, 1))"
            />
          </div>
          <AppTextArea
            :model-value="rule.instructions"
            :label="t('requestAudit.instructions')"
            :disabled="disabled"
            @update:model-value="update((v) => (v.rules[index]!.instructions = $event))"
          />
          <AppTextField
            :model-value="String(rule.threshold)"
            inputmode="decimal"
            :label="t('requestAudit.threshold')"
            :description="t('requestAudit.thresholdHelp')"
            :disabled="disabled"
            @update:model-value="update((v) => (v.rules[index]!.threshold = Number($event)))"
          />
        </div>
      </AppFormSection>
    </template>
    <AppNotice>{{ t('requestAudit.coverageHelp') }}</AppNotice>
  </div>
</template>

<style scoped>
.modern-request-audit,
.modern-request-audit-rule {
  display: grid;
  gap: var(--modern-space-4);
}
.modern-request-audit-rule {
  padding-block: var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-request-audit-rule-heading {
  display: flex;
  align-items: end;
  gap: var(--modern-space-3);
}
.modern-request-audit-rule-heading > :first-child {
  flex: 1;
  min-width: 0;
}
</style>
