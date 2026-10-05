<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Plus, Trash2 } from '@lucide/vue'
import { AppActionMenu, AppButton, AppIconButton, AppTextField } from '@modern/components/ui'
import PolicyModelInput from './PolicyModelInput.vue'
import { isMultiplierRaw, type VisualAction } from './policy-model'
import { usePolicyDraftStatus } from './use-policy-draft'
import { usePolicyMessages } from './use-policy-messages'

const props = withDefaults(
  defineProps<{
    actions: VisualAction[]
    disabled?: boolean
  }>(),
  { disabled: false },
)

const emit = defineEmits<{
  update: [actions: VisualAction[]]
}>()
const { t } = usePolicyMessages()

interface ActionSlot {
  key: number
  draft: string
  stored: string
}
let nextActionKey = 0
const slots = ref<ActionSlot[]>([])

watch(
  () => props.actions,
  (list) => {
    const previous = slots.value
    slots.value = list.map((action, index) => {
      const stored = action.type === 'multiply_price' ? action.factor : '1'
      const slot = previous[index]
      if (!slot) return { key: ++nextActionKey, draft: stored, stored }
      return { key: slot.key, draft: slot.draft === slot.stored ? stored : slot.draft, stored }
    })
  },
  { immediate: true, deep: true },
)

const blocks = computed(() =>
  props.actions.map((action, index) => ({
    action,
    slot: slots.value[index] ?? { key: -index, draft: '1', stored: '1' },
  })),
)

const hasFactorError = computed(() =>
  props.actions.some(
    (action, index) =>
      action.type === 'multiply_price' && !isMultiplierRaw(slots.value[index]?.draft ?? '1'),
  ),
)

const hasPendingFactor = computed(() =>
  props.actions.some(
    (action, index) =>
      action.type === 'multiply_price' && action.factor !== (slots.value[index]?.draft ?? ''),
  ),
)

usePolicyDraftStatus('action', () => ({
  active: !props.disabled,
  valid: !hasFactorError.value,
  pending: hasPendingFactor.value,
}))

function actionLabel(type: VisualAction['type']): string {
  if (type === 'multiply_price') return t('policyEditor.action.multiplyPrice') + '：'
  if (type === 'exclude_models') return t('policyEditor.action.excludeModels') + '：'
  return t('policyEditor.action.excludeCandidate') + '：'
}

function helpText(type: VisualAction['type']): string {
  if (type === 'multiply_price') return t('policyEditor.action.help.multiplyPrice')
  if (type === 'exclude_models') return t('policyEditor.action.help.excludeModels')
  return t('policyEditor.action.help.excludeCandidate')
}

function commitActions(actions: VisualAction[]): void {
  if (props.disabled) return
  emit('update', actions)
}

function updateActionAt(index: number, action: VisualAction): void {
  commitActions(props.actions.map((item, at) => (at === index ? action : item)))
}

function commitFactor(index: number): void {
  const draft = slots.value[index]?.draft
  if (draft === undefined || !isMultiplierRaw(draft)) return
  updateActionAt(index, { type: 'multiply_price', factor: draft })
}

// 遵循 Latest Apply 原则，无需做防卫性置灰禁用
const addMenuItems = computed(() => [
  { id: 'exclude_models', label: t('policyEditor.action.excludeModels') },
  { id: 'multiply_price', label: t('policyEditor.action.multiplyPrice') },
])

function appendFromMenu(id: unknown): void {
  if (props.disabled) return
  const action: VisualAction =
    id === 'multiply_price'
      ? { type: 'multiply_price', factor: '1' }
      : { type: 'exclude_models', models: [] }
  commitActions([...props.actions, action])
}

function removeAction(index: number): void {
  if (props.disabled || props.actions.length <= 1) return
  slots.value.splice(index, 1)
  commitActions(props.actions.filter((_, at) => at !== index))
}
</script>

<template>
  <div class="policy-action">
    <div class="policy-action-list">
      <div v-for="(block, index) in blocks" :key="block.slot.key" class="policy-action-block">
        <div class="policy-action-block-content">
          <div class="policy-action-row">
            <span class="policy-action-type-label">
              {{ actionLabel(block.action.type) }}
            </span>
            <div class="policy-action-control">
              <AppTextField
                v-if="block.action.type === 'multiply_price'"
                v-model="block.slot.draft"
                class="policy-action-factor"
                :label="t('policyEditor.action.factor')"
                label-hidden
                :placeholder="t('policyEditor.action.factorPlaceholder')"
                size="xs"
                :error="
                  block.slot.draft && !isMultiplierRaw(block.slot.draft)
                    ? t('policyEditor.action.factorInvalid')
                    : undefined
                "
                :disabled="disabled"
                @change="() => commitFactor(index)"
                @blur="() => commitFactor(index)"
              />
              <PolicyModelInput
                v-else-if="block.action.type === 'exclude_models'"
                :model-value="block.action.models"
                class="policy-action-models"
                size="xs"
                :disabled="disabled"
                @update:model-value="(models) => updateActionAt(index, { type: 'exclude_models', models })"
              />
            </div>
          </div>
          <p v-if="helpText(block.action.type)" class="modern-hint">
            {{ helpText(block.action.type) }}
          </p>
        </div>

        <AppIconButton
          v-if="actions.length > 1"
          class="policy-action-delete"
          :icon="Trash2"
          :label="t('policyEditor.action.removeAction')"
          size="xs"
          :disabled="disabled"
          @click="removeAction(index)"
        />
      </div>
    </div>

    <div class="policy-action-add">
      <AppActionMenu
        :label="t('policyEditor.action.addAction')"
        :items="addMenuItems"
        size="xs"
        :disabled="disabled"
        @select="appendFromMenu"
      >
        <template #trigger>
          <AppButton size="xs" variant="ghost" :icon="Plus" :disabled="disabled">
            {{ t('policyEditor.action.addAction') }}
          </AppButton>
        </template>
      </AppActionMenu>
    </div>
  </div>
</template>

<style scoped>
.policy-action {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-2);
}
.policy-action-list {
  display: flex;
  flex-direction: column;
}
.policy-action-block {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--modern-space-2);
  padding-bottom: var(--modern-space-2);
  margin-bottom: var(--modern-space-2);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.policy-action-block:last-child {
  border-bottom: none;
  margin-bottom: 0;
  padding-bottom: 0;
}
.policy-action-block-content {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-1);
}
.policy-action-row {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  width: 100%;
}
.policy-action-type-label {
  flex: 0 0 auto;
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
  color: var(--modern-muted);
  white-space: nowrap;
}
.policy-action-control {
  flex: 1;
  min-width: 0;
}
.policy-action-factor {
  max-width: 14rem;
}
.policy-action-models {
  width: 100%;
}
.policy-action-delete {
  flex: none;
  margin-top: var(--modern-space-0-5);
}
.policy-action-add {
  display: flex;
  align-items: center;
}
@media (max-width: 760px) {
  .policy-action-row {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
