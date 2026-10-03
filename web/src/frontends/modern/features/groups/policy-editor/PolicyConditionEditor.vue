<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ArrowDown, ArrowUp, Plus, Trash2 } from '@lucide/vue'
import {
  AppActionMenu,
  AppButton,
  AppIconButton,
  AppNotice,
  AppSelect,
} from '@modern/components/ui'
import {
  arrayItems,
  arrayNode,
  conditionKind,
  getField,
  literalString,
  newFactCondition,
  newTimeWindowCondition,
  objectNode,
  serializeJson,
  setField,
  type JsonNode,
  type JsonObjectNode,
} from './policy-model'
import { usePolicyMessages } from './use-policy-messages'
import PolicyFactLeaf from './PolicyFactLeaf.vue'
import PolicyTimeWindowLeaf from './PolicyTimeWindowLeaf.vue'

const props = withDefaults(
  defineProps<{ modelValue: JsonNode | undefined; disabled?: boolean }>(),
  {
    disabled: false,
  },
)
const emit = defineEmits<{ 'update:modelValue': [node: JsonNode] }>()
const { t } = usePolicyMessages()

const object = computed<JsonObjectNode | undefined>(() =>
  props.modelValue && props.modelValue.type === 'object' ? props.modelValue : undefined,
)
const groupKind = computed<'all' | 'any' | undefined>(() => {
  const kind = conditionKind(props.modelValue)
  return kind === 'all' || kind === 'any' ? kind : undefined
})
const children = computed<JsonNode[]>(() => {
  const kind = groupKind.value
  const current = object.value
  if (!kind || !current) return []
  return arrayItems(getField(current, kind)) ?? []
})

const blockKeys = ref<number[]>([])
let nextBlockKey = 0
watch(
  () => children.value.length,
  (count) => {
    if (count !== blockKeys.value.length) {
      blockKeys.value = Array.from({ length: count }, () => nextBlockKey++)
    }
  },
  { immediate: true },
)

function leafKind(node: JsonNode | undefined): 'param' | 'time_window' | 'unsupported' {
  const kind = conditionKind(node)
  if (kind === 'param')
    return literalString(getField(node, 'op')) === 'in' ? 'unsupported' : 'param'
  if (kind === 'time_window') return 'time_window'
  return 'unsupported'
}

const soloLeaf = computed(() => {
  const current = object.value
  if (!current || groupKind.value) return undefined
  const kind = leafKind(current)
  return kind === 'param' || kind === 'time_window' ? { node: current, kind } : undefined
})

const blocks = computed(() =>
  groupKind.value ? children.value.map((node) => ({ node, kind: leafKind(node) })) : [],
)

function commit(next: JsonNode[]): void {
  const kind = groupKind.value
  const current = object.value
  if (!kind || !current) return
  emit('update:modelValue', setField(current, kind, arrayNode(next)))
}

function changeGroup(next: unknown): void {
  if (props.disabled || (next !== 'all' && next !== 'any') || next === groupKind.value) return
  emit('update:modelValue', objectNode([{ key: next, value: arrayNode(children.value) }]))
}

function updateChild(index: number, node: JsonNode): void {
  if (props.disabled) return
  commit(children.value.map((item, at) => (at === index ? node : item)))
}

function removeChild(index: number): void {
  if (props.disabled) return
  blockKeys.value.splice(index, 1)
  commit(children.value.filter((_, at) => at !== index))
}

function moveChild(index: number, delta: number): void {
  if (props.disabled) return
  const list = children.value
  const target = index + delta
  if (target < 0 || target >= list.length) return
  const next = [...list]
  const [item] = next.splice(index, 1)
  next.splice(target, 0, item)
  const [key] = blockKeys.value.splice(index, 1)
  blockKeys.value.splice(target, 0, key)
  commit(next)
}

const groupOptions = computed(() => [
  { value: 'all', label: t('policyEditor.condition.all') },
  { value: 'any', label: t('policyEditor.condition.any') },
])
const addItems = computed(() => [
  { id: 'param', label: t('policyEditor.condition.itemParam') },
  { id: 'time_window', label: t('policyEditor.condition.itemTimeWindow') },
])
function appendFromMenu(id: unknown): void {
  if (props.disabled || (id !== 'param' && id !== 'time_window')) return
  const node = id === 'time_window' ? newTimeWindowCondition() : newFactCondition('request.model')
  blockKeys.value.push(nextBlockKey++)
  commit([...children.value, node])
}
</script>

<template>
  <div class="policy-condition">
    <template v-if="groupKind && object">
      <div class="policy-condition-toolbar">
        <span class="policy-condition-toolbar-label">{{ t('policyEditor.condition.title') }}</span>
        <AppSelect
          :model-value="groupKind"
          :options="groupOptions"
          :label="t('policyEditor.condition.title')"
          label-hidden
          size="sm"
          :disabled="disabled"
          @update:model-value="changeGroup"
        />
      </div>

      <div v-if="blocks.length" class="policy-condition-blocks">
        <div
          v-for="(block, index) in blocks"
          :key="blockKeys[index]"
          class="policy-condition-block"
        >
          <div class="policy-condition-block-head">
            <AppIconButton
              :icon="ArrowUp"
              :label="t('policyEditor.condition.moveUp')"
              size="sm"
              :disabled="disabled || index === 0"
              @click="moveChild(index, -1)"
            />
            <AppIconButton
              :icon="ArrowDown"
              :label="t('policyEditor.condition.moveDown')"
              size="sm"
              :disabled="disabled || index === blocks.length - 1"
              @click="moveChild(index, 1)"
            />
            <AppIconButton
              :icon="Trash2"
              :label="t('policyEditor.condition.remove')"
              size="sm"
              variant="danger"
              :disabled="disabled"
              @click="removeChild(index)"
            />
          </div>
          <PolicyFactLeaf
            v-if="block.kind === 'param' && block.node.type === 'object'"
            :model-value="block.node"
            :disabled="disabled"
            @update:model-value="(node) => updateChild(index, node)"
          />
          <PolicyTimeWindowLeaf
            v-else-if="block.kind === 'time_window' && block.node.type === 'object'"
            :model-value="block.node"
            :disabled="disabled"
            @update:model-value="(node) => updateChild(index, node)"
          />
          <pre v-else class="policy-condition-json font-mono">{{ serializeJson(block.node) }}</pre>
        </div>
      </div>
      <p v-else class="policy-condition-empty">{{ t('policyEditor.condition.empty') }}</p>

      <div class="policy-condition-add">
        <AppActionMenu
          :label="t('policyEditor.condition.addCondition')"
          :items="addItems"
          size="sm"
          :disabled="disabled"
          @select="appendFromMenu"
        >
          <template #trigger>
            <AppButton size="sm" :icon="Plus" :disabled="disabled">
              {{ t('policyEditor.condition.addCondition') }}
            </AppButton>
          </template>
        </AppActionMenu>
      </div>
    </template>

    <template v-else-if="soloLeaf">
      <PolicyFactLeaf
        v-if="soloLeaf.kind === 'param'"
        :model-value="soloLeaf.node"
        :disabled="disabled"
        @update:model-value="(node) => emit('update:modelValue', node)"
      />
      <PolicyTimeWindowLeaf
        v-else
        :model-value="soloLeaf.node"
        :disabled="disabled"
        @update:model-value="(node) => emit('update:modelValue', node)"
      />
    </template>

    <template v-else>
      <AppNotice tone="info" compact>{{ t('policyEditor.condition.unsupported') }}</AppNotice>
      <pre class="policy-condition-json font-mono">{{
        props.modelValue ? serializeJson(props.modelValue) : ''
      }}</pre>
    </template>
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
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-semibold);
}
.policy-condition-blocks {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-condition-block {
  display: grid;
  gap: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  padding: var(--modern-space-2);
  min-width: 0;
}
.policy-condition-block-head {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--modern-space-1);
}
.policy-condition-add {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.policy-condition-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
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
</style>
