<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AppSelect, AppTextField } from '@modern/components/ui'
import {
  factDefinition,
  formatQuotaWindow,
  getField,
  isRatioRaw,
  literalNumberRaw,
  literalString,
  newFactCondition,
  newQuotaFactCondition,
  numberLiteral,
  quotaFactKey,
  quotaWindowFromToken,
  quotaWindowToken,
  setField,
  stringLiteral,
  type JsonNode,
  type JsonObjectNode,
} from './policy-model'
import { usePolicyDraftReporter } from './use-policy-draft'
import { usePolicyMessages } from './use-policy-messages'

const props = withDefaults(
  defineProps<{
    modelValue: JsonObjectNode
    disabled?: boolean
    quotaWindows?: readonly number[]
  }>(),
  { disabled: false, quotaWindows: () => [] },
)
const emit = defineEmits<{ 'update:modelValue': [node: JsonObjectNode] }>()
const { t } = usePolicyMessages()

const factRaw = computed(() => literalString(getField(props.modelValue, 'fact')) ?? '')
const value = computed(() => getField(props.modelValue, 'value'))
const selectNode = computed(() => getField(props.modelValue, 'select'))

// 额度条件用 `quota:<seconds>` token 选择；落盘仍是 canonical fact + select/reduce。
const quotaWindow = computed(() =>
  factRaw.value === quotaFactKey
    ? (literalNumberRaw(getField(selectNode.value, 'window_seconds')) ?? '')
    : '',
)
const selected = computed(() =>
  quotaWindow.value ? quotaWindowToken(quotaWindow.value) : factRaw.value,
)
const definition = computed(() => factDefinition(quotaWindow.value ? quotaFactKey : factRaw.value))
const op = computed(() => literalString(getField(props.modelValue, 'op')) ?? '')

const catalogWindows = computed(() =>
  Array.from(new Set(props.quotaWindows.map((seconds) => String(seconds)))),
)
const modelFactKeys = ['request.model', 'upstream.model'] as const
const windowChoices = computed(() => {
  const list = [...catalogWindows.value].sort((a, b) => Number(a) - Number(b))
  const saved = quotaWindow.value
  // 已保存但当前 catalog 未观测到的窗口必须保留同值项，绝不自动改写既有 source/window。
  if (saved && !list.includes(saved)) list.push(saved)
  return list
})
const factOptions = computed(() => [
  ...modelFactKeys.map((key) => ({ value: key, label: t(factDefinition(key)!.labelKey) })),
  ...windowChoices.value.map((raw) => ({
    value: quotaWindowToken(raw),
    label: `${t(factDefinition(quotaFactKey)!.labelKey)} · ${formatQuotaWindow(raw)}`,
  })),
])
const opOptions = computed(() =>
  (definition.value?.operators ?? []).map((item) => ({
    value: item,
    label: t(`policyEditor.fact.operators.${item}`),
  })),
)
const factHint = computed(() => {
  if (quotaWindow.value) {
    return catalogWindows.value.includes(quotaWindow.value)
      ? t('policyEditor.fact.descriptions.quotaRemainingRatio')
      : t('policyEditor.fact.descriptions.quotaRetained')
  }
  return factRaw.value === 'upstream.model'
    ? t('policyEditor.fact.descriptions.upstreamModel')
    : t('policyEditor.fact.descriptions.requestModel')
})

function replaceFact(next: unknown): void {
  if (props.disabled || typeof next !== 'string') return
  const window = quotaWindowFromToken(next)
  if (window) {
    // 同一额度参数换窗口只改 window_seconds，保留比较符与阈值。
    if (quotaWindow.value) {
      const select = selectNode.value
      if (!select || select.type !== 'object') return
      const literal = numberLiteral(window)
      if (literal) {
        emit(
          'update:modelValue',
          setField(props.modelValue, 'select', setField(select, 'window_seconds', literal)),
        )
      }
      return
    }
    emit('update:modelValue', newQuotaFactCondition(window))
    return
  }
  if (next !== selected.value) emit('update:modelValue', newFactCondition(next))
}

function emitValue(next: JsonNode): void {
  if (props.disabled) return
  emit('update:modelValue', setField(props.modelValue, 'value', next))
}

function replaceOperator(next: unknown): void {
  if (props.disabled || typeof next !== 'string') return
  emit('update:modelValue', setField(props.modelValue, 'op', stringLiteral(next)))
}

const stringValue = computed({
  get: () => literalString(value.value) ?? '',
  set: (next: string) => emitValue(stringLiteral(next)),
})

const ratioRaw = computed(() => literalNumberRaw(value.value) ?? '')
const ratioDraft = ref(ratioRaw.value)
watch(ratioRaw, (next) => {
  ratioDraft.value = next
})
const ratioInvalid = computed(() => !isRatioRaw(ratioDraft.value))
const { report: reportDraft } = usePolicyDraftReporter('fact')

watch(
  [ratioDraft, ratioRaw, ratioInvalid, quotaWindow, () => props.disabled],
  () => {
    if (props.disabled || !quotaWindow.value) {
      reportDraft(true, false)
      return
    }
    const isPending = ratioDraft.value !== ratioRaw.value
    const isValid = !ratioInvalid.value
    reportDraft(isValid, isPending)
  },
  { immediate: true },
)

function commitRatio(): void {
  if (props.disabled || ratioInvalid.value) return
  const literal = numberLiteral(ratioDraft.value)
  if (literal) emitValue(literal)
}
</script>

<template>
  <div class="policy-fact">
    <div class="policy-fact-labels" aria-hidden="true">
      <span>{{ t('policyEditor.fact.key') }}</span>
      <span>{{ t('policyEditor.fact.operator') }}</span>
      <span>{{ t('policyEditor.fact.value') }}</span>
    </div>
    <div class="policy-fact-row">
      <AppSelect
        :model-value="selected"
        :options="factOptions"
        :label="t('policyEditor.fact.key')"
        label-hidden
        size="xs"
        :disabled="disabled"
        @update:model-value="replaceFact"
      />
      <AppSelect
        :model-value="op"
        :options="opOptions"
        :label="t('policyEditor.fact.operator')"
        label-hidden
        size="xs"
        :disabled="disabled"
        @update:model-value="replaceOperator"
      />
      <AppTextField
        v-if="definition && definition.valueType === 'string'"
        v-model="stringValue"
        :label="t('policyEditor.fact.value')"
        label-hidden
        :placeholder="t('policyEditor.fact.valuePlaceholder')"
        size="xs"
        :disabled="disabled"
      />
      <AppTextField
        v-else
        v-model="ratioDraft"
        :label="t('policyEditor.fact.value')"
        label-hidden
        :error="ratioInvalid ? t('policyEditor.fact.errors.ratioInvalid') : undefined"
        size="xs"
        :disabled="disabled"
        @change="commitRatio"
        @blur="commitRatio"
      />
    </div>
    <p v-if="factHint" class="policy-fact-hint">{{ factHint }}</p>
  </div>
</template>

<style scoped>
.policy-fact {
  display: grid;
  gap: var(--modern-space-1);
  min-width: 0;
}
.policy-fact-labels,
.policy-fact-row {
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) 6rem minmax(0, 1fr);
  gap: var(--modern-space-2);
}
.policy-fact-labels {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.policy-fact-row > :deep(*) {
  min-width: 0;
}
.policy-fact-hint {
  margin: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  line-height: var(--modern-leading-body);
}
@media (max-width: 760px) {
  .policy-fact-labels,
  .policy-fact-row {
    grid-template-columns: minmax(0, 1.3fr) 5rem minmax(0, 1fr);
  }
}
</style>
