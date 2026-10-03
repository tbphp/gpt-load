<script setup lang="ts">
import { computed, ref } from 'vue'
import { Plus } from '@lucide/vue'
import { AppBadge, AppButton, AppNotice, AppSelect } from '@modern/components/ui'
import {
  arrayItems,
  arrayNode,
  conditionKind,
  getField,
  newConditionGroup,
  newFactCondition,
  newNotCondition,
  newTimeWindowCondition,
  policyLimits,
  serializeJson,
  setField,
  type ConditionKind,
  type JsonNode,
} from './policy-model'
import { usePolicyMessages } from './use-policy-messages'
import PolicyFactLeaf from './PolicyFactLeaf.vue'
import PolicyTimeWindowLeaf from './PolicyTimeWindowLeaf.vue'

const props = withDefaults(
  defineProps<{
    modelValue: JsonNode | undefined
    depth: number
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [node: JsonNode] }>()
const { t } = usePolicyMessages()

const kind = computed<ConditionKind>(() => conditionKind(props.modelValue))
// 后端深度上限。叶子只占 1 层，分组会自带一个默认叶子、占 2 层，因此两道闸门不同。
const maxDepth = policyLimits.maxConditionDepth
const leafDepthBlocked = computed(() => props.depth + 1 > maxDepth)
const groupDepthBlocked = computed(() => props.depth + 2 > maxDepth)
const node = computed(() => props.modelValue)
const objectNode = computed(() =>
  props.modelValue && props.modelValue.type === 'object' ? props.modelValue : undefined,
)

function build(next: string): JsonNode {
  if (next === 'all') return newConditionGroup('all')
  if (next === 'any') return newConditionGroup('any')
  if (next === 'not') return newNotCondition()
  if (next === 'time_window') return newTimeWindowCondition()
  return newFactCondition('request.model')
}

function existingChildren(): JsonNode[] {
  if (!node.value) return []
  if (kind.value === 'all' || kind.value === 'any') return children.value
  if (kind.value === 'not') return notChild.value ? [notChild.value] : []
  return [node.value]
}

// 转换形态不丢弃内容：分组↔分组保留子项，其它形态整体包裹为新分组的子节点。
function buildPreserving(next: string): JsonNode {
  if (next === 'not') return node.value ? newNotCondition(node.value) : newNotCondition()
  return newConditionGroup(next === 'any' ? 'any' : 'all', existingChildren())
}

function replaceKind(next: unknown): void {
  if (props.disabled || typeof next !== 'string' || next === kind.value) return
  // 改为分组会把现有子节点留在 depth+1，触及上限时不允许。
  if (leafDepthBlocked.value && (next === 'all' || next === 'any' || next === 'not')) return
  // param/time_window 是叶子终点形态，改型会丢掉字段；UI 已置灰，这里也不再替换。
  if (next === 'param' || next === 'time_window') return
  emit('update:modelValue', buildPreserving(next))
}

const children = computed(() => {
  if (!node.value || (kind.value !== 'all' && kind.value !== 'any')) return []
  return arrayItems(getField(node.value, kind.value as 'all' | 'any')) ?? []
})

function commitChildren(next: JsonNode[]): void {
  const current = objectNode.value
  const groupKind = kind.value
  if (!current || (groupKind !== 'all' && groupKind !== 'any')) return
  emit('update:modelValue', setField(current, groupKind, arrayNode(next)))
}

function updateChild(index: number, child: JsonNode): void {
  if (props.disabled) return
  commitChildren(children.value.map((item, at) => (at === index ? child : item)))
}

function removeChild(index: number): void {
  if (props.disabled) return
  commitChildren(children.value.filter((_, at) => at !== index))
}

const notChild = computed(() => (node.value ? getField(node.value, 'not') : undefined))

function updateNot(updated: JsonNode): void {
  if (props.disabled) return
  const current = objectNode.value
  if (current) emit('update:modelValue', setField(current, 'not', updated))
}

const addKind = ref('time_window')
// 新增分组的子节点会在 depth+1，其默认叶子在 depth+2；新增单个叶子只到 depth+1。
const appendBlocked = computed(() =>
  addKind.value === 'all' || addKind.value === 'any' || addKind.value === 'not'
    ? groupDepthBlocked.value
    : leafDepthBlocked.value,
)
const addOptions = computed(() => [
  { value: 'param', label: t('policyEditor.condition.addFact'), disabled: leafDepthBlocked.value },
  {
    value: 'time_window',
    label: t('policyEditor.condition.addTimeWindow'),
    disabled: leafDepthBlocked.value,
  },
  // 分组选项保持可选，由下方按钮的 appendBlocked 拦下超深的新增，便于用户看出是哪一项被限制。
  { value: 'all', label: t('policyEditor.condition.addAll') },
  { value: 'any', label: t('policyEditor.condition.addAny') },
  { value: 'not', label: t('policyEditor.condition.addNot') },
])

function appendChild(): void {
  if (props.disabled || appendBlocked.value) return
  commitChildren([...children.value, build(addKind.value)])
}

const kindOptions = computed(() => {
  const leafDisabled = (value: 'param' | 'time_window') => kind.value !== value
  const options = [
    { value: 'all', label: t('policyEditor.condition.all'), disabled: leafDepthBlocked.value },
    { value: 'any', label: t('policyEditor.condition.any'), disabled: leafDepthBlocked.value },
    { value: 'not', label: t('policyEditor.condition.not'), disabled: leafDepthBlocked.value },
    { value: 'param', label: t('policyEditor.condition.addFact'), disabled: leafDisabled('param') },
    {
      value: 'time_window',
      label: t('policyEditor.condition.addTimeWindow'),
      disabled: leafDisabled('time_window'),
    },
  ]
  if (kind.value === 'unsupported') {
    return [
      { value: 'unsupported', label: t('policyEditor.condition.unsupported'), disabled: false },
      ...options,
    ]
  }
  return options
})
</script>

<template>
  <div v-if="node" class="policy-condition" :data-depth="depth">
    <div class="policy-condition-toolbar">
      <AppBadge tone="neutral" compact>{{ t('policyEditor.condition.kindLabel') }}</AppBadge>
      <AppSelect
        :model-value="kind"
        :options="kindOptions"
        :label="t('policyEditor.condition.kindLabel')"
        label-hidden
        size="xs"
        :disabled="disabled"
        @update:model-value="replaceKind"
      />
      <span v-if="kind === 'all' || kind === 'any'" class="policy-condition-count">
        {{ t('policyEditor.condition.childrenCount', { count: children.length }) }}
      </span>
    </div>

    <!-- 且 / 或 -->
    <div v-if="kind === 'all' || kind === 'any'" class="policy-condition-children">
      <div v-for="(child, index) in children" :key="index" class="policy-condition-child">
        <PolicyConditionEditor
          :model-value="child"
          :depth="depth + 1"
          :disabled="disabled"
          @update:model-value="(updated) => updateChild(index, updated)"
        />
        <AppButton variant="text" size="xxs" :disabled="disabled" @click="removeChild(index)">
          {{ t('policyEditor.condition.remove') }}
        </AppButton>
      </div>
      <div class="policy-condition-add">
        <AppSelect
          v-model="addKind"
          :options="addOptions"
          :label="t('policyEditor.condition.addCondition')"
          label-hidden
          size="xs"
          :disabled="disabled || leafDepthBlocked"
        />
        <AppButton
          size="sm"
          :icon="Plus"
          :disabled="disabled || appendBlocked"
          @click="appendChild"
        >
          {{ t('policyEditor.condition.addCondition') }}
        </AppButton>
      </div>
    </div>

    <!-- 非 -->
    <div v-else-if="kind === 'not'" class="policy-condition-children">
      <PolicyConditionEditor
        v-if="notChild"
        :model-value="notChild"
        :depth="depth + 1"
        :disabled="disabled"
        @update:model-value="updateNot"
      />
    </div>

    <!-- 参数比较 -->
    <PolicyFactLeaf
      v-else-if="kind === 'param' && objectNode"
      :model-value="objectNode"
      :disabled="disabled"
      @update:model-value="(updated) => emit('update:modelValue', updated)"
    />

    <!-- 时间段 -->
    <PolicyTimeWindowLeaf
      v-else-if="kind === 'time_window' && objectNode"
      :model-value="objectNode"
      :disabled="disabled"
      @update:model-value="(updated) => emit('update:modelValue', updated)"
    />

    <!-- 仅支持 JSON 的结构 -->
    <template v-else>
      <AppNotice tone="info" compact>{{ t('policyEditor.condition.unsupported') }}</AppNotice>
      <pre class="policy-condition-json font-mono">{{ node ? serializeJson(node) : '' }}</pre>
    </template>

    <p
      v-if="groupDepthBlocked && (kind === 'all' || kind === 'any')"
      class="policy-condition-depth"
    >
      {{ t('policyEditor.condition.depthLimited', { max: maxDepth }) }}
    </p>
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
.policy-condition-count {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.policy-condition-children {
  display: grid;
  gap: var(--modern-space-2);
  border-left: var(--modern-line-width) solid var(--modern-border);
  padding-left: var(--modern-space-3);
  min-width: 0;
}
.policy-condition-child {
  display: grid;
  gap: var(--modern-space-1);
  justify-items: start;
  min-width: 0;
}
.policy-condition-add {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.policy-condition-json {
  overflow-x: auto;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  padding: var(--modern-space-2);
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
}
.policy-condition-depth {
  color: var(--modern-warning);
  font-size: var(--modern-font-size-caption);
}
</style>
