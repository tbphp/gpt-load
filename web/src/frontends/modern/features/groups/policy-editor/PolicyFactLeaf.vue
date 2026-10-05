<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AppSelect, AppTextField } from '@modern/components/ui'
import PolicyModelInput from './PolicyModelInput.vue'
import {
  factDefinition,
  formatQuotaWindow,
  isRatio,
  newFactCondition,
  newQuotaFactCondition,
  quotaFactKey,
  quotaWindowFromToken,
  quotaWindowToken,
  type VisualParamCondition,
} from './policy-model'
import { usePolicyDraftStatus } from './use-policy-draft'
import { usePolicyMessages } from './use-policy-messages'

const props = withDefaults(
  defineProps<{
    condition: VisualParamCondition
    disabled?: boolean
    quotaWindows?: readonly number[]
  }>(),
  { disabled: false, quotaWindows: () => [] },
)
const emit = defineEmits<{ update: [condition: VisualParamCondition] }>()
const { t } = usePolicyMessages()

// 额度条件用 `quota:<seconds>` token 选择；落盘仍是 canonical fact + select/reduce。
const quotaWindow = computed(() =>
  props.condition.fact === quotaFactKey && props.condition.windowSeconds !== undefined
    ? String(props.condition.windowSeconds)
    : '',
)
const selected = computed(() =>
  quotaWindow.value ? quotaWindowToken(quotaWindow.value) : props.condition.fact,
)
const definition = computed(() =>
  factDefinition(quotaWindow.value ? quotaFactKey : props.condition.fact),
)

const catalogWindows = computed(() =>
  Array.from(new Set(props.quotaWindows.map((seconds) => String(seconds)))),
)
const modelFactKeys = ['request.model', 'upstream.model'] as const
const windowChoices = computed(() => {
  const list = [...catalogWindows.value].sort((a, b) => Number(a) - Number(b))
  const saved = quotaWindow.value
  // 已保存但当前 catalog 未观测到的窗口必须保留同值项，绝不自动改写既有 window。
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
  return props.condition.fact === 'upstream.model'
    ? t('policyEditor.fact.descriptions.upstreamModel')
    : t('policyEditor.fact.descriptions.requestModel')
})

function replaceFact(next: unknown): void {
  if (props.disabled || typeof next !== 'string') return
  const window = quotaWindowFromToken(next)
  if (window) {
    // 同一额度参数换窗口只改 windowSeconds，保留比较符与阈值。
    if (quotaWindow.value) {
      emit('update', { ...props.condition, windowSeconds: Number(window) })
    } else {
      emit('update', newQuotaFactCondition(Number(window)))
    }
    return
  }
  if (next !== selected.value) emit('update', newFactCondition(next))
}

function emitValue(value: string | number | string[]): void {
  if (props.disabled) return
  emit('update', { ...props.condition, value })
}

function replaceOperator(next: unknown): void {
  if (props.disabled || typeof next !== 'string') return
  // eq <-> in 互转必须无损保留用户输入，并在一次更新内同时切换 op 与 value：
  // 单值转单元素数组，数组取首项回填，绝不产生 op 与 value 类型不一致的非法组合。
  if (next === 'in') {
    const value = Array.isArray(props.condition.value)
      ? [...props.condition.value]
      : [typeof props.condition.value === 'string' ? props.condition.value : '']
    emit('update', { ...props.condition, op: next, value })
    return
  }
  if (props.condition.op === 'in') {
    const items = Array.isArray(props.condition.value) ? props.condition.value : []
    emit('update', { ...props.condition, op: next, value: items[0] ?? '' })
    return
  }
  emit('update', { ...props.condition, op: next })
}

const modelList = computed<string[]>({
  get: () => (Array.isArray(props.condition.value) ? [...props.condition.value] : []),
  set: (value: string[]) => emitValue([...value]),
})

const stringValue = computed({
  get: () => (typeof props.condition.value === 'string' ? props.condition.value : ''),
  set: (value: string) => emitValue(value),
})

const ratioRaw = computed(() =>
  typeof props.condition.value === 'number' ? String(props.condition.value) : '',
)
const ratioDraft = ref(ratioRaw.value)
watch(ratioRaw, (next) => {
  ratioDraft.value = next
})
const ratioInvalid = computed(
  () => ratioDraft.value.trim() === '' || !isRatio(Number(ratioDraft.value)),
)
usePolicyDraftStatus('fact', () => ({
  active: !props.disabled && Boolean(quotaWindow.value),
  valid: !ratioInvalid.value,
  pending: ratioDraft.value !== ratioRaw.value,
}))

function commitRatio(): void {
  if (props.disabled || ratioInvalid.value) return
  const next = Number(ratioDraft.value)
  // 数值未变但字面量不同（已有 0.1 输入 0.10 后失焦）：显式同步草稿以清除 pending，避免保存被永久阻塞。
  if (ratioRaw.value !== '' && next === Number(ratioRaw.value)) {
    ratioDraft.value = ratioRaw.value
    return
  }
  emitValue(next)
}
</script>

<template>
  <div class="policy-fact">
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
        :model-value="condition.op"
        :options="opOptions"
        :label="t('policyEditor.fact.operator')"
        label-hidden
        size="xs"
        :disabled="disabled"
        @update:model-value="replaceOperator"
      />
      <PolicyModelInput
        v-if="definition && definition.valueType === 'string' && condition.op === 'in'"
        :model-value="modelList"
        size="xs"
        :disabled="disabled"
        @update:model-value="(models) => (modelList = models)"
      />
      <AppTextField
        v-else-if="definition && definition.valueType === 'string'"
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
    <p v-if="factHint" class="modern-hint">{{ factHint }}</p>
  </div>
</template>

<style scoped>
.policy-fact {
  display: grid;
  gap: var(--modern-space-1);
  min-width: 0;
}
.policy-fact-row {
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) 6rem minmax(0, 1fr);
  gap: var(--modern-space-2);
}
.policy-fact-row > :deep(*) {
  min-width: 0;
}
@media (max-width: 760px) {
  .policy-fact-row {
    grid-template-columns: minmax(0, 1.3fr) 5rem minmax(0, 1fr);
  }
}
</style>
