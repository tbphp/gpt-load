<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Plus, Trash2 } from '@lucide/vue'
import {
  AppActionMenu,
  AppButton,
  AppIconButton,
  AppSelect,
  AppTextField,
} from '@modern/components/ui'
import { newCondition, type PolicyMatch, type VisualCondition } from './policy-model'
import { usePolicyMessages } from './use-policy-messages'
import PolicyFactLeaf from './PolicyFactLeaf.vue'
import PolicyTimeWindowLeaf from './PolicyTimeWindowLeaf.vue'

const props = withDefaults(
  defineProps<{
    match: PolicyMatch
    conditions: VisualCondition[]
    disabled?: boolean
    quotaWindows?: readonly number[]
  }>(),
  { disabled: false, quotaWindows: () => [] },
)
const emit = defineEmits<{
  update: [value: { match: PolicyMatch; conditions: VisualCondition[] }]
}>()
const { t } = usePolicyMessages()

const blockKeys = ref<number[]>([])
let nextBlockKey = 0
watch(
  () => props.conditions.length,
  (count) => {
    if (count !== blockKeys.value.length) {
      blockKeys.value = Array.from({ length: count }, () => nextBlockKey++)
    }
  },
  { immediate: true },
)

function commit(conditions: VisualCondition[], match: PolicyMatch = props.match): void {
  if (props.disabled) return
  emit('update', { match, conditions })
}

function changeMatch(next: unknown): void {
  if (props.disabled || (next !== 'all' && next !== 'any') || next === props.match) return
  emit('update', { match: next, conditions: props.conditions })
}

function updateChild(index: number, condition: VisualCondition): void {
  commit(props.conditions.map((item, at) => (at === index ? condition : item)))
}

function removeChild(index: number): void {
  if (props.disabled) return
  blockKeys.value.splice(index, 1)
  commit(props.conditions.filter((_, at) => at !== index))
}

function appendFromMenu(id: unknown): void {
  if (props.disabled || (id !== 'param' && id !== 'time_window')) return
  blockKeys.value.push(nextBlockKey++)
  commit([...props.conditions, newCondition(id)])
}

function blockTitle(condition: VisualCondition): string {
  if (condition.kind === 'time_window') return t('policyEditor.condition.itemTimeWindow')
  if (condition.kind === 'expression') return t('policyEditor.condition.itemExpression')
  return t('policyEditor.condition.itemParam')
}

const groupOptions = computed(() => [
  { value: 'all', label: t('policyEditor.condition.all') },
  { value: 'any', label: t('policyEditor.condition.any') },
])
// expression 条件后端暂不支持，菜单不提供入口；已存在的 expression 条件仍可编辑展示。
const addItems = computed(() => [
  { id: 'param', label: t('policyEditor.condition.itemParam') },
  { id: 'time_window', label: t('policyEditor.condition.itemTimeWindow') },
])
</script>

<template>
  <div class="policy-condition">
    <div class="policy-condition-toolbar">
      <span class="policy-condition-toolbar-label">{{ t('policyEditor.condition.title') }}</span>
      <AppSelect
        :model-value="match"
        :options="groupOptions"
        :label="t('policyEditor.condition.title')"
        label-hidden
        size="xs"
        :disabled="disabled"
        @update:model-value="changeMatch"
      />
    </div>

    <div v-if="conditions.length" class="policy-condition-blocks">
      <div
        v-for="(condition, index) in conditions"
        :key="blockKeys[index]"
        class="policy-condition-block"
      >
        <div class="policy-condition-block-row">
          <span class="modern-sr-only">{{ blockTitle(condition) }}</span>
          <div class="policy-condition-block-main">
            <PolicyFactLeaf
              v-if="condition.kind === 'param'"
              :condition="condition"
              :disabled="disabled"
              :quota-windows="quotaWindows"
              @update="(next) => updateChild(index, next)"
            />
            <PolicyTimeWindowLeaf
              v-else-if="condition.kind === 'time_window'"
              :condition="condition"
              :disabled="disabled"
              @update="(next) => updateChild(index, next)"
            />
            <AppTextField
              v-else
              :model-value="condition.expression"
              :label="t('policyEditor.condition.expression')"
              :placeholder="t('policyEditor.condition.expressionPlaceholder')"
              label-hidden
              size="xs"
              :disabled="disabled"
              @update:model-value="
                (value) => updateChild(index, { kind: 'expression', expression: value })
              "
            />
          </div>
          <AppIconButton
            class="policy-condition-delete"
            :icon="Trash2"
            :label="t('policyEditor.condition.remove')"
            size="xs"
            :disabled="disabled"
            @click="removeChild(index)"
          />
        </div>
      </div>
    </div>

    <div class="policy-condition-add">
      <AppActionMenu
        :label="t('policyEditor.condition.addCondition')"
        :items="addItems"
        size="xs"
        :disabled="disabled"
        @select="appendFromMenu"
      >
        <template #trigger>
          <AppButton size="xs" variant="ghost" :icon="Plus" :disabled="disabled">
            {{ t('policyEditor.condition.addCondition') }}
          </AppButton>
        </template>
      </AppActionMenu>
    </div>
  </div>
</template>

<style scoped>
.policy-condition {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-condition-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.policy-condition-toolbar-label {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.policy-condition-blocks {
  display: grid;
  gap: 0;
  min-width: 0;
}
.policy-condition-block {
  padding: var(--modern-space-2) 0;
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  min-width: 0;
}
.policy-condition-block:first-child {
  padding-top: var(--modern-space-1);
}
.policy-condition-block:last-child {
  border-bottom: none;
  padding-bottom: var(--modern-space-1);
}
.policy-condition-block-row {
  display: flex;
  align-items: flex-start;
  gap: var(--modern-space-2);
  min-width: 0;
  width: 100%;
}
.policy-condition-block-main {
  flex: 1 1 0%;
  min-width: 0;
}
.policy-condition-delete {
  flex: 0 0 auto;
  margin-top: var(--modern-space-0-5);
}
.policy-condition-add {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
</style>
