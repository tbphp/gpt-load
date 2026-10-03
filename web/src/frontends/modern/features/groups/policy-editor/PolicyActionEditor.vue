<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AppNotice, AppSelect, AppTextField } from '@modern/components/ui'
import {
  actionRepresentable,
  getField,
  isMultiplierRaw,
  literalString,
  objectNode,
  serializeJson,
  setField,
  stringLiteral,
  type JsonNode,
  type JsonObjectNode,
  type PolicyDomain,
} from './policy-model'
import { usePolicyMessages } from './use-policy-messages'

const props = withDefaults(
  defineProps<{
    modelValue: JsonNode | undefined
    domain: PolicyDomain | ''
    disabled?: boolean
  }>(),
  { disabled: false },
)
// 动作切换必须与 domain 一起原子更新，因此把两者打包交给规则父级统一提交。
const emit = defineEmits<{
  'update:action': [payload: { domain: PolicyDomain; then: JsonObjectNode }]
}>()
const { t } = usePolicyMessages()

// 非契约形态（未知动作、额外/缺失字段）整块 JSON-only，绝不隐式改写成已知动作。
const representable = computed(() => actionRepresentable(props.modelValue, props.domain))
const factorRaw = computed(() => literalString(getField(props.modelValue, 'factor')) ?? '')
const factorDraft = ref(factorRaw.value)
watch(factorRaw, (next) => {
  factorDraft.value = next
})
const factorInvalid = computed(() => !isMultiplierRaw(factorDraft.value))

// 仅在 representable 时渲染选单，因此这里必定是已知动作类型。
const kind = computed(() => {
  const type = literalString(getField(props.modelValue, 'type'))
  if (type === 'multiply_price') return 'multiply_price'
  if (type === 'exclude_candidate') return 'exclude_candidate'
  return props.domain === 'pricing' ? 'multiply_price' : 'exclude_candidate'
})
const kindOptions = computed(() => [
  { label: t('policyEditor.action.excludeCandidate'), value: 'exclude_candidate' },
  { label: t('policyEditor.action.multiplyPrice'), value: 'multiply_price' },
])
const helpText = computed(() =>
  kind.value === 'multiply_price'
    ? t('policyEditor.action.help.multiplyPrice')
    : t('policyEditor.action.help.excludeCandidate'),
)

function emitAction(domain: PolicyDomain, then: JsonObjectNode): void {
  if (props.disabled) return
  emit('update:action', { domain, then })
}

function selectKind(value: string): void {
  if (props.disabled) return
  if (value === 'multiply_price') {
    const factor = isMultiplierRaw(factorDraft.value) ? factorDraft.value : '1'
    emitAction(
      'pricing',
      objectNode([
        { key: 'type', value: stringLiteral('multiply_price') },
        { key: 'factor', value: stringLiteral(factor) },
      ]),
    )
    return
  }
  emitAction('scheduling', objectNode([{ key: 'type', value: stringLiteral('exclude_candidate') }]))
}

function commitFactor(): void {
  const current = props.modelValue
  if (props.disabled || !representable.value || factorInvalid.value) return
  if (!current || current.type !== 'object') return
  emitAction('pricing', setField(current, 'factor', stringLiteral(factorDraft.value)))
}
</script>

<template>
  <div class="policy-action">
    <template v-if="representable">
      <div class="policy-action-row">
        <span class="policy-action-label">{{ t('policyEditor.action.title') }}</span>
        <AppSelect
          :model-value="kind"
          :options="kindOptions"
          class="policy-action-select"
          :label="t('policyEditor.action.title')"
          label-hidden
          size="sm"
          :disabled="disabled"
          @update:model-value="selectKind"
        />
        <AppTextField
          v-if="kind === 'multiply_price'"
          v-model="factorDraft"
          class="policy-action-factor"
          :label="t('policyEditor.action.factor')"
          label-hidden
          :placeholder="t('policyEditor.action.factorPlaceholder')"
          :error="factorInvalid ? t('policyEditor.action.factorInvalid') : undefined"
          :disabled="disabled"
          @change="commitFactor"
        />
      </div>
      <p class="policy-action-help">{{ helpText }}</p>
    </template>

    <template v-else>
      <AppNotice tone="info" compact>{{ t('policyEditor.condition.unsupported') }}</AppNotice>
      <pre class="policy-action-json font-mono">{{
        modelValue ? serializeJson(modelValue) : ''
      }}</pre>
    </template>
  </div>
</template>

<style scoped>
.policy-action {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-action-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-action-label {
  flex-shrink: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.policy-action-select {
  flex: 0 1 10rem;
  min-width: 0;
}
.policy-action-factor {
  flex: 0 1 7rem;
  min-width: 0;
}
.policy-action-help {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.policy-action-json {
  overflow-x: auto;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  padding: var(--modern-space-2);
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
}
</style>
