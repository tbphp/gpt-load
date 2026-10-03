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
  defineProps<{
    modelValue: JsonNode | undefined
    disabled?: boolean
    quotaWindows?: readonly number[]
  }>(),
  {
    disabled: false,
    quotaWindows: () => [],
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
  if (!current) return []
  if (kind) return arrayItems(getField(current, kind)) ?? []
  return leafKind(current) === 'unsupported' ? [] : [current]
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
  if (kind === 'param') return 'param'
  if (kind === 'time_window') return 'time_window'
  return 'unsupported'
}

const editable = computed(() => !!groupKind.value || children.value.length > 0)
const blocks = computed(() => children.value.map((node) => ({ node, kind: leafKind(node) })))

function blockTitle(kind: 'param' | 'time_window' | 'unsupported'): string {
  if (kind === 'time_window') return t('policyEditor.condition.itemTimeWindow')
  if (kind === 'param') return t('policyEditor.condition.itemParam')
  return t('policyEditor.condition.jsonLabel')
}

function commit(next: JsonNode[]): void {
  if (props.disabled) return
  const kind = groupKind.value
  emit(
    'update:modelValue',
    kind && object.value
      ? setField(object.value, kind, arrayNode(next))
      : next.length === 1
        ? next[0]!
        : objectNode([{ key: 'all', value: arrayNode(next) }]),
  )
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
    <template v-if="editable">
      <div class="policy-condition-toolbar">
        <span class="policy-condition-toolbar-label">{{ t('policyEditor.condition.title') }}</span>
        <AppSelect
          :model-value="groupKind ?? 'all'"
          :options="groupOptions"
          :label="t('policyEditor.condition.title')"
          label-hidden
          size="xs"
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
            <span class="policy-condition-block-title">{{ blockTitle(block.kind) }}</span>
            <div class="policy-condition-block-actions">
              <div class="policy-condition-block-move">
                <AppIconButton
                  :icon="ArrowUp"
                  :label="t('policyEditor.condition.moveUp')"
                  size="xs"
                  :disabled="disabled || index === 0"
                  @click="moveChild(index, -1)"
                />
                <AppIconButton
                  :icon="ArrowDown"
                  :label="t('policyEditor.condition.moveDown')"
                  size="xs"
                  :disabled="disabled || index === blocks.length - 1"
                  @click="moveChild(index, 1)"
                />
              </div>
              <AppIconButton
                :icon="Trash2"
                :label="t('policyEditor.condition.remove')"
                size="xs"
                :disabled="disabled"
                @click="removeChild(index)"
              />
            </div>
          </div>
          <PolicyFactLeaf
            v-if="block.kind === 'param' && block.node.type === 'object'"
            :model-value="block.node"
            :disabled="disabled"
            :quota-windows="quotaWindows"
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
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
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
  justify-content: space-between;
  gap: var(--modern-space-1);
}
.policy-condition-block-title {
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
  color: var(--modern-muted);
}
.policy-condition-block-actions {
  display: flex;
  align-items: center;
  gap: 0;
}
.policy-condition-block-move {
  display: flex;
  align-items: center;
  gap: 0;
  margin-right: var(--modern-space-1);
  padding-right: var(--modern-space-1);
  border-right: var(--modern-line-width) solid var(--modern-border);
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
</style>
