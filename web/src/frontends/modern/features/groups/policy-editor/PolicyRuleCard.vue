<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, Copy, Trash2 } from '@lucide/vue'
import {
  AppButton,
  AppIconButton,
  AppOverflowText,
  AppSwitch,
  AppTextField,
} from '@modern/components/ui'
import { policyLimits, type VisualRule } from './policy-model'
import { usePolicyDraftStatus } from './use-policy-draft'
import { usePolicyMessages } from './use-policy-messages'
import PolicyActionEditor from './PolicyActionEditor.vue'
import PolicyConditionEditor from './PolicyConditionEditor.vue'

const props = withDefaults(
  defineProps<{
    rule: VisualRule
    index: number
    total: number
    disabled?: boolean
    quotaWindows?: readonly number[]
  }>(),
  { disabled: false, quotaWindows: () => [] },
)
const emit = defineEmits<{
  update: [rule: VisualRule]
  move: [delta: number]
  duplicate: []
  remove: []
}>()
const { t } = usePolicyMessages()
const expanded = ref(!props.disabled)
const opened = ref(expanded.value)
watch(expanded, (value) => {
  if (value) opened.value = true
})

const name = computed({
  get: () => props.rule.name,
  set: (value: string) => patch({ name: value }),
})

const nameInvalid = computed(
  () =>
    name.value.trim() === '' ||
    name.value !== name.value.trim() ||
    [...name.value].length > policyLimits.maxNameLength,
)

usePolicyDraftStatus('rule-name', () => ({
  active: !props.disabled,
  valid: !nameInvalid.value,
  pending: false,
}))

function patch(changes: Partial<VisualRule>): void {
  if (props.disabled) return
  emit('update', { ...props.rule, ...changes })
}

function patchEnabled(value: boolean): void {
  patch({ enabled: value })
}
</script>

<template>
  <article class="policy-rule-card">
    <header class="policy-rule-heading">
      <AppButton
        class="policy-rule-toggle"
        variant="ghost"
        size="xs"
        :icon="expanded ? ChevronDown : ChevronRight"
        :aria-expanded="expanded"
        @click="expanded = !expanded"
      >
        <span>{{ t('policyEditor.rule.number', { number: index + 1 }) }}</span>
        <AppOverflowText :text="rule.name || rule.id || t('policyEditor.rule.name')" />
      </AppButton>
      <div class="policy-rule-tools">
        <span class="policy-rule-enabled">
          <span>{{ t('policyEditor.rule.enabled') }}</span>
          <AppSwitch
            :model-value="rule.enabled"
            :label="t('policyEditor.rule.enabled')"
            size="sm"
            :disabled="disabled"
            @update:model-value="patchEnabled"
          />
        </span>
        <div class="policy-rule-move">
          <AppIconButton
            :icon="ArrowUp"
            :label="t('policyEditor.rule.moveUp')"
            size="xs"
            :disabled="disabled || index === 0"
            @click="emit('move', -1)"
          />
          <AppIconButton
            :icon="ArrowDown"
            :label="t('policyEditor.rule.moveDown')"
            size="xs"
            :disabled="disabled || index === total - 1"
            @click="emit('move', 1)"
          />
        </div>
        <AppIconButton
          :icon="Copy"
          :label="t('policyEditor.rule.duplicate')"
          size="xs"
          :disabled="disabled || total >= policyLimits.maxRulesPerConfig"
          @click="emit('duplicate')"
        />
        <AppIconButton
          :icon="Trash2"
          :label="t('policyEditor.rule.remove')"
          size="xs"
          :disabled="disabled"
          @click="!disabled && emit('remove')"
        />
      </div>
    </header>

    <div v-if="opened" v-show="expanded" class="policy-rule-body">
      <div class="policy-rule-row">
        <span class="policy-rule-row-label">{{ t('policyEditor.rule.name') }}</span>
        <AppTextField
          v-model="name"
          class="policy-rule-row-control"
          :label="t('policyEditor.rule.name')"
          label-hidden
          :placeholder="t('policyEditor.rule.namePlaceholder')"
          size="sm"
          :error="
            nameInvalid
              ? t('policyEditor.rule.nameInvalid', { max: policyLimits.maxNameLength })
              : undefined
          "
          :disabled="disabled"
        />
      </div>
      <p class="modern-hint">{{ t('policyEditor.rule.disabledHint') }}</p>

      <PolicyConditionEditor
        :match="rule.match"
        :conditions="rule.conditions"
        :disabled="disabled"
        :quota-windows="quotaWindows"
        @update="(group) => patch({ match: group.match, conditions: group.conditions })"
      />

      <PolicyActionEditor
        :actions="rule.actions"
        :disabled="disabled"
        @update="(actions) => patch({ actions })"
      />
    </div>
  </article>
</template>

<style scoped>
.policy-rule-card {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
}
.policy-rule-heading {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  padding: var(--modern-space-1);
  background: var(--modern-subtle);
  border-radius: var(--modern-radius-control);
}
.policy-rule-toggle {
  flex: 1;
  min-width: 0;
  justify-content: flex-start;
  text-align: left;
}
.policy-rule-tools {
  display: flex;
  align-items: center;
  flex: none;
  gap: 0;
}
.policy-rule-enabled {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-2);
  margin-right: var(--modern-space-1);
  padding-right: var(--modern-space-1);
  border-right: var(--modern-line-width) solid var(--modern-border);
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.policy-rule-move {
  display: flex;
  align-items: center;
  gap: 0;
  margin-right: var(--modern-space-1);
  padding-right: var(--modern-space-1);
  border-right: var(--modern-line-width) solid var(--modern-border);
}
.policy-rule-body {
  display: grid;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2);
  min-width: 0;
}
.policy-rule-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  min-width: 0;
}
.policy-rule-row-label {
  flex-shrink: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.policy-rule-row-control {
  flex: 1 1 12rem;
  min-width: 0;
}
</style>
