<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronDown, ChevronRight, ChevronUp, Copy, Trash2 } from '@lucide/vue'
import {
  AppBadge,
  AppButton,
  AppIconButton,
  AppNotice,
  AppSwitch,
  AppTextField,
} from '@modern/components/ui'
import {
  booleanLiteral,
  conditionKind,
  policyLimits,
  setField,
  stringLiteral,
  type JsonNode,
  type JsonObjectNode,
  type PolicyDomain,
  type VisualRule,
} from './policy-model'
import { usePolicyMessages } from './use-policy-messages'
import PolicyActionEditor from './PolicyActionEditor.vue'
import PolicyConditionEditor from './PolicyConditionEditor.vue'

const props = withDefaults(
  defineProps<{
    rule: VisualRule
    index: number
    total: number
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{
  update: [node: JsonObjectNode]
  move: [delta: number]
  duplicate: []
  remove: []
}>()
const { t } = usePolicyMessages()
const expanded = ref(true)
const confirmingRemove = ref(false)

// 只读态一旦生效，待确认的删除必须撤销，且所有变更入口都被阻断。
watch(
  () => props.disabled,
  (value) => {
    if (value) confirmingRemove.value = false
  },
)

const name = computed({
  get: () => props.rule.name,
  set: (value: string) => patch('name', stringLiteral(value)),
})
const enabled = computed(() => props.rule.enabled)

const nameInvalid = computed(
  () =>
    props.rule.name.trim() === '' ||
    props.rule.name !== props.rule.name.trim() ||
    [...props.rule.name].length > policyLimits.maxNameLength,
)
const domainKey = computed(() =>
  props.rule.domain === 'pricing' ? 'domainPricing' : 'domainScheduling',
)

function patch(field: string, value: JsonNode): void {
  if (props.disabled) return
  emit('update', setField(props.rule.node, field, value))
}

function patchEnabled(value: boolean): void {
  if (props.disabled) return
  patch('enabled', booleanLiteral(value))
}

// 动作选择同时改动 domain 与 then，必须一次性提交，避免出现不可编译的中间态。
function patchAction(payload: { domain: PolicyDomain; then: JsonNode }): void {
  if (props.disabled) return
  const withThen = setField(props.rule.node, 'then', payload.then)
  emit('update', setField(withThen, 'domain', stringLiteral(payload.domain)))
}
</script>

<template>
  <article class="policy-rule-card">
    <header class="policy-rule-header">
      <AppButton
        variant="ghost"
        size="sm"
        :icon="expanded ? ChevronDown : ChevronRight"
        @click="expanded = !expanded"
      >
        <span class="policy-rule-title">
          {{ t('policyEditor.rule.number', { number: index + 1 }) }}
          <span class="policy-rule-name">{{
            rule.name || rule.id || t('policyEditor.rule.name')
          }}</span>
        </span>
      </AppButton>
      <AppBadge tone="neutral" compact>{{ t(`policyEditor.rule.${domainKey}`) }}</AppBadge>
      <span class="policy-rule-enabled">
        <AppSwitch
          :model-value="enabled"
          :label="t('policyEditor.rule.enabled')"
          size="sm"
          :disabled="disabled"
          @update:model-value="patchEnabled"
        />
      </span>
      <div class="policy-rule-actions">
        <AppIconButton
          :icon="ChevronUp"
          :label="t('policyEditor.rule.moveUp')"
          size="sm"
          :disabled="disabled || index === 0"
          @click="emit('move', -1)"
        />
        <AppIconButton
          :icon="ChevronDown"
          :label="t('policyEditor.rule.moveDown')"
          size="sm"
          :disabled="disabled || index === total - 1"
          @click="emit('move', 1)"
        />
        <AppIconButton
          :icon="Copy"
          :label="t('policyEditor.rule.duplicate')"
          size="sm"
          :disabled="disabled"
          @click="emit('duplicate')"
        />
        <AppIconButton
          :icon="Trash2"
          :label="t('policyEditor.rule.remove')"
          size="sm"
          variant="danger"
          :disabled="disabled"
          @click="!disabled && (confirmingRemove = true)"
        />
      </div>
    </header>

    <AppNotice v-if="confirmingRemove" tone="warning" class="policy-rule-remove">
      <span>{{ t('policyEditor.rule.removeConfirm', { name: rule.name || rule.id }) }}</span>
      <span class="policy-rule-remove-actions">
        <AppButton size="sm" :disabled="disabled" @click="confirmingRemove = false">
          {{ t('policyEditor.rule.cancel') }}
        </AppButton>
        <AppButton
          size="sm"
          variant="danger"
          :disabled="disabled"
          @click="
            () => {
              confirmingRemove = false
              if (!disabled) emit('remove')
            }
          "
        >
          {{ t('policyEditor.rule.confirmRemove') }}
        </AppButton>
      </span>
    </AppNotice>

    <div v-if="expanded" class="policy-rule-body">
      <div class="policy-rule-row">
        <span class="policy-rule-row-label">{{ t('policyEditor.rule.name') }}</span>
        <AppTextField
          v-model="name"
          class="policy-rule-row-control"
          :label="t('policyEditor.rule.name')"
          label-hidden
          :placeholder="t('policyEditor.rule.namePlaceholder')"
          :error="
            nameInvalid
              ? t('policyEditor.rule.nameInvalid', { max: policyLimits.maxNameLength })
              : undefined
          "
          :disabled="disabled"
        />
      </div>
      <p class="policy-rule-disabled-hint">{{ t('policyEditor.rule.disabledHint') }}</p>

      <PolicyConditionEditor
        :model-value="rule.when"
        :disabled="disabled"
        @update:model-value="(node) => patch('when', node)"
      />

      <PolicyActionEditor
        :model-value="rule.then"
        :domain="rule.domain"
        :disabled="disabled"
        @update:action="patchAction"
      />

      <AppNotice v-if="conditionKind(rule.when) === 'unsupported'" tone="info" compact>
        {{ t('policyEditor.rule.unsupportedRule') }}
      </AppNotice>
    </div>
  </article>
</template>

<style scoped>
.policy-rule-card {
  display: grid;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-3);
  min-width: 0;
}
.policy-rule-header {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.policy-rule-title {
  display: inline-flex;
  align-items: baseline;
  gap: var(--modern-space-2);
  font-weight: var(--modern-weight-medium);
}
.policy-rule-name {
  color: var(--modern-muted);
  font-weight: var(--modern-weight-regular);
}
.policy-rule-enabled {
  display: inline-flex;
  align-items: center;
}
.policy-rule-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  margin-left: auto;
}
.policy-rule-remove {
  flex-direction: column;
  align-items: flex-start;
}
.policy-rule-remove-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.policy-rule-body {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
.policy-rule-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-rule-row-label {
  flex-shrink: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.policy-rule-row-control {
  flex: 1 1 12rem;
  min-width: 0;
}
.policy-rule-disabled-hint {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
</style>
