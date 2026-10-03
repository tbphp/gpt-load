<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Plus, X } from '@lucide/vue'
import { AppButton, AppIconButton, AppSelect, AppTextField } from '@modern/components/ui'
import {
  arrayItems,
  arrayNode,
  factDefinition,
  factDefinitions,
  getField,
  isRatioRaw,
  literalNumberRaw,
  literalString,
  newFactCondition,
  numberLiteral,
  policyLimits,
  setField,
  stringLiteral,
  type JsonNode,
  type JsonObjectNode,
} from './policy-model'
import { usePolicyMessages } from './use-policy-messages'

const props = withDefaults(defineProps<{ modelValue: JsonObjectNode; disabled?: boolean }>(), {
  disabled: false,
})
const emit = defineEmits<{ 'update:modelValue': [node: JsonObjectNode] }>()
const { t } = usePolicyMessages()

const factRaw = computed(() => literalString(getField(props.modelValue, 'fact')) ?? '')
const definition = computed(() => factDefinition(factRaw.value))
const op = computed(() => literalString(getField(props.modelValue, 'op')) ?? '')
const value = computed(() => getField(props.modelValue, 'value'))
const selectNode = computed(() => getField(props.modelValue, 'select'))

const factOptions = computed(() =>
  factDefinitions.map((item) => ({ value: item.key, label: t(item.labelKey) })),
)
const opOptions = computed(() =>
  (definition.value?.operators ?? []).map((item) => ({
    value: item,
    label: t(`policyEditor.fact.operators.${item}`),
  })),
)

function replaceFact(next: unknown): void {
  if (props.disabled) return
  if (typeof next === 'string') emit('update:modelValue', newFactCondition(next))
}

function emitValue(next: JsonNode): void {
  if (props.disabled) return
  emit('update:modelValue', setField(props.modelValue, 'value', next))
}

function replaceOperator(next: unknown): void {
  if (props.disabled || typeof next !== 'string' || !definition.value) return
  if (definition.value.valueType === 'string') {
    const current = inItems.value
    const single = literalString(value.value) ?? ''
    const nextValue =
      next === 'in'
        ? arrayNode([stringLiteral(current[0] ?? single)])
        : stringLiteral(single || current[0] || '')
    emit(
      'update:modelValue',
      setField(setField(props.modelValue, 'op', stringLiteral(next)), 'value', nextValue),
    )
    return
  }
  emit('update:modelValue', setField(props.modelValue, 'op', stringLiteral(next)))
}

// 字符串参数：单值
const stringValue = computed({
  get: () => literalString(value.value) ?? '',
  set: (next: string) => emitValue(stringLiteral(next)),
})

// 字符串参数：in 集合
const inItems = computed(() =>
  (arrayItems(value.value) ?? []).map((item) => literalString(item) ?? ''),
)
function setInItem(index: number, next: string): void {
  emitValue(arrayNode(inItems.value.map((item, at) => stringLiteral(at === index ? next : item))))
}
function addInItem(): void {
  emitValue(arrayNode([...inItems.value, ''].map((item) => stringLiteral(item))))
}
function removeInItem(index: number): void {
  emitValue(
    arrayNode(inItems.value.filter((_, at) => at !== index).map((item) => stringLiteral(item))),
  )
}
function modelError(item: string): string | undefined {
  if (item.trim() === '') return t('policyEditor.fact.errors.modelRequired')
  if ([...item].length > policyLimits.maxModelLength)
    return t('policyEditor.fact.errors.modelTooLong', { max: policyLimits.maxModelLength })
  if (inItems.value.filter((value) => value === item).length > 1)
    return t('policyEditor.fact.errors.modelDuplicate')
  return undefined
}

// 额度：窗口秒数
const windowRaw = computed(
  () => literalNumberRaw(getField(selectNode.value, 'window_seconds')) ?? '',
)
const windowDraft = ref(windowRaw.value)
watch(windowRaw, (next) => {
  windowDraft.value = next
})
const windowInvalid = computed(() => !/^[1-9]\d*$/.test(windowDraft.value))
function commitWindow(): void {
  if (props.disabled) return
  const select = selectNode.value
  if (windowInvalid.value || !select || select.type !== 'object') return
  const literal = numberLiteral(windowDraft.value)
  if (literal) {
    emit(
      'update:modelValue',
      setField(props.modelValue, 'select', setField(select, 'window_seconds', literal)),
    )
  }
}

// 额度：比例阈值。保留导入原文，直到用户显式修改。
const ratioRaw = computed(() => literalNumberRaw(value.value) ?? '')
const ratioDraft = ref(ratioRaw.value)
watch(ratioRaw, (next) => {
  ratioDraft.value = next
})
const ratioInvalid = computed(() => !isRatioRaw(ratioDraft.value))
function commitRatio(): void {
  if (props.disabled || ratioInvalid.value) return
  const literal = numberLiteral(ratioDraft.value)
  if (literal) emitValue(literal)
}
</script>

<template>
  <div class="policy-fact">
    <div class="policy-fact-row">
      <AppSelect
        :model-value="factRaw"
        :options="factOptions"
        :label="t('policyEditor.fact.key')"
        size="sm"
        :disabled="disabled"
        @update:model-value="replaceFact"
      />
      <AppSelect
        :model-value="op"
        :options="opOptions"
        :label="t('policyEditor.fact.operator')"
        size="sm"
        :disabled="disabled"
        @update:model-value="replaceOperator"
      />
    </div>

    <!-- 字符串单值 -->
    <AppTextField
      v-if="definition && definition.valueType === 'string' && op === 'eq'"
      v-model="stringValue"
      :label="t('policyEditor.fact.value')"
      :placeholder="t('policyEditor.fact.valuePlaceholder')"
      :disabled="disabled"
    />

    <!-- 字符串集合 -->
    <div v-else-if="definition && definition.valueType === 'string'" class="policy-fact-list">
      <div v-for="(item, index) in inItems" :key="index" class="policy-fact-list-row">
        <AppTextField
          :model-value="item"
          :label="`${t('policyEditor.fact.value')} ${index + 1}`"
          :error="modelError(item)"
          :disabled="disabled"
          @update:model-value="(next) => setInItem(index, next)"
        />
        <AppIconButton
          :icon="X"
          :label="t('policyEditor.fact.removeValue')"
          size="sm"
          :disabled="disabled"
          @click="removeInItem(index)"
        />
      </div>
      <AppButton size="sm" variant="outline" :icon="Plus" :disabled="disabled" @click="addInItem">
        {{ t('policyEditor.fact.addValue') }}
      </AppButton>
    </div>

    <!-- 额度比较 -->
    <template v-else-if="definition && definition.quota">
      <div class="policy-fact-quota">
        <span class="policy-fact-static">
          {{ t('policyEditor.fact.quota.scope') }}: {{ t('policyEditor.fact.quota.scopeAccount') }}
        </span>
        <AppTextField
          v-model="windowDraft"
          :label="t('policyEditor.fact.quota.windowSeconds')"
          :error="windowInvalid ? t('policyEditor.fact.errors.windowInvalid') : undefined"
          :disabled="disabled"
          @change="commitWindow"
        />
        <span class="policy-fact-static">
          {{ t('policyEditor.fact.quota.reduce') }}:
          {{ t('policyEditor.fact.quota.reduceMin') }}
        </span>
      </div>
      <AppTextField
        v-model="ratioDraft"
        :label="t('policyEditor.fact.value')"
        :error="ratioInvalid ? t('policyEditor.fact.errors.ratioInvalid') : undefined"
        :disabled="disabled"
        @change="commitRatio"
      />
    </template>
  </div>
</template>

<style scoped>
.policy-fact {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-fact-row {
  display: flex;
  align-items: flex-end;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.policy-fact-row > :deep(*) {
  min-width: 0;
  flex: 1 1 12rem;
}
.policy-fact-list {
  display: grid;
  gap: var(--modern-space-2);
}
.policy-fact-list-row {
  display: flex;
  align-items: flex-end;
  gap: var(--modern-space-2);
}
.policy-fact-list-row > :deep(*) {
  flex: 1;
  min-width: 0;
}
.policy-fact-quota {
  display: grid;
  gap: var(--modern-space-2);
  grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
}
.policy-fact-static {
  align-self: center;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
</style>
