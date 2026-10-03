<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AppSelect, AppTextField } from '@modern/components/ui'
import {
  factDefinition,
  factDefinitions,
  getField,
  isRatioRaw,
  literalNumberRaw,
  literalString,
  newFactCondition,
  numberLiteral,
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
const opOptions = computed(() => {
  const operators = definition.value?.operators ?? []
  return operators.map((item) => ({ value: item, label: t(`policyEditor.fact.operators.${item}`) }))
})

function replaceFact(next: unknown): void {
  if (props.disabled || typeof next !== 'string') return
  emit('update:modelValue', newFactCondition(next))
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

const windowRaw = computed(
  () => literalNumberRaw(getField(selectNode.value, 'window_seconds')) ?? '',
)
const windowDraft = ref(windowRaw.value)
watch(windowRaw, (next) => {
  windowDraft.value = next
})
const windowInvalid = computed(() => !/^[1-9]\d*$/.test(windowDraft.value))
function commitWindow(): void {
  if (props.disabled || windowInvalid.value) return
  const select = selectNode.value
  if (!select || select.type !== 'object') return
  const literal = numberLiteral(windowDraft.value)
  if (literal) {
    emit(
      'update:modelValue',
      setField(props.modelValue, 'select', setField(select, 'window_seconds', literal)),
    )
  }
}

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
      <AppTextField
        v-if="definition && definition.valueType === 'string'"
        v-model="stringValue"
        :label="t('policyEditor.fact.value')"
        :placeholder="t('policyEditor.fact.valuePlaceholder')"
        :disabled="disabled"
      />
      <AppTextField
        v-else
        v-model="ratioDraft"
        :label="t('policyEditor.fact.value')"
        :error="ratioInvalid ? t('policyEditor.fact.errors.ratioInvalid') : undefined"
        :disabled="disabled"
        @change="commitRatio"
      />
    </div>

    <div v-if="definition?.quota" class="policy-fact-window">
      <AppTextField
        v-model="windowDraft"
        :label="t('policyEditor.fact.quota.windowSeconds')"
        :error="windowInvalid ? t('policyEditor.fact.errors.windowInvalid') : undefined"
        :disabled="disabled"
        @change="commitWindow"
      />
    </div>
  </div>
</template>

<style scoped>
.policy-fact {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-fact-row {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(0, 0.8fr) minmax(0, 1fr);
  align-items: flex-end;
  gap: var(--modern-space-2);
}
.policy-fact-row > :deep(*) {
  min-width: 0;
}
.policy-fact-window {
  max-width: 14rem;
}
@media (max-width: 760px) {
  .policy-fact-row {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
