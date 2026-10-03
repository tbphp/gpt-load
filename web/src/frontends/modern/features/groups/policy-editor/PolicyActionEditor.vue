<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AppNotice, AppTextField } from '@modern/components/ui'
import {
  actionRepresentable,
  getField,
  isMultiplierRaw,
  literalString,
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
const emit = defineEmits<{ 'update:modelValue': [node: JsonObjectNode] }>()
const { t } = usePolicyMessages()

// 非契约形态（未知动作、额外/缺失字段）整块 JSON-only，绝不隐式改写成已知动作。
const representable = computed(() => actionRepresentable(props.modelValue, props.domain))
const factorRaw = computed(() => literalString(getField(props.modelValue, 'factor')) ?? '')
const factorDraft = ref(factorRaw.value)
watch(factorRaw, (next) => {
  factorDraft.value = next
})
const factorInvalid = computed(() => !isMultiplierRaw(factorDraft.value))

function normalizeDecimal(raw: string): string {
  const dot = raw.indexOf('.')
  const integer = (dot < 0 ? raw : raw.slice(0, dot)).replace(/^0+(?=\d)/, '')
  const fraction = dot < 0 ? '' : raw.slice(dot + 1).replace(/0+$/, '')
  if (integer === '0' && fraction === '') return '0'
  return fraction ? `${integer}.${fraction}` : integer
}
const factorIsZero = computed(
  () => isMultiplierRaw(factorDraft.value) && normalizeDecimal(factorDraft.value) === '0',
)
const factorIsOne = computed(
  () => isMultiplierRaw(factorDraft.value) && normalizeDecimal(factorDraft.value) === '1',
)

function commitFactor(): void {
  if (props.disabled || !representable.value || factorInvalid.value) return
  const current = props.modelValue
  if (!current || current.type !== 'object') return
  emit('update:modelValue', setField(current, 'factor', stringLiteral(factorDraft.value)))
}
</script>

<template>
  <div class="policy-action">
    <template v-if="representable && domain === 'pricing'">
      <p class="policy-action-label">{{ t('policyEditor.action.multiplyPrice') }}</p>
      <AppTextField
        v-model="factorDraft"
        :label="t('policyEditor.action.factor')"
        :placeholder="t('policyEditor.action.factorPlaceholder')"
        :error="factorInvalid ? t('policyEditor.action.factorInvalid') : undefined"
        :disabled="disabled"
        @change="commitFactor"
      />
      <AppNotice v-if="factorIsZero" tone="warning" compact>
        {{ t('policyEditor.action.factorZero') }}
      </AppNotice>
      <AppNotice v-else-if="factorIsOne" tone="info" compact>
        {{ t('policyEditor.action.factorOne') }}
      </AppNotice>
      <p class="policy-action-hint">{{ t('policyEditor.action.multiplyPriceHelp') }}</p>
      <p class="policy-action-hint">{{ t('policyEditor.action.cumulative') }}</p>
    </template>

    <template v-else-if="representable && domain === 'scheduling'">
      <p class="policy-action-label">{{ t('policyEditor.action.excludeCandidate') }}</p>
      <p class="policy-action-hint">{{ t('policyEditor.action.excludeCandidateHelp') }}</p>
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
.policy-action-label {
  font-weight: var(--modern-weight-medium);
  font-size: var(--modern-font-size-secondary);
}
.policy-action-hint {
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
