<script setup lang="ts">
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, Copy, Plus, Trash2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  errorRuleDraft,
  errorRuleValue,
  errorRuleErrors,
  errorRuleRetries,
  errorRuleEffects,
  hasErrorRuleCooldown,
  validErrorRules,
  type ErrorRule,
} from '@shared/error-rules'
import {
  AppBadge,
  AppButton,
  AppIconButton,
  AppNotice,
  AppOverflowText,
  AppSelect,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'

const props = defineProps<{ modelValue: ErrorRule[]; disabled?: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: ErrorRule[]]
  'update:valid': [value: boolean]
}>()
const { t, n } = useI18n()
let nextKey = 0
const rows = ref(props.modelValue.map((rule) => errorRuleDraft(rule, nextKey++)))
const values = computed(() => rows.value.map(errorRuleValue))
const valid = computed(() => validErrorRules(values.value))
let synchronized = JSON.stringify(values.value)
let emitted = JSON.stringify(props.modelValue)
watch(valid, (value) => emit('update:valid', value), { immediate: true })
watch(
  values,
  (value) => {
    const encoded = JSON.stringify(value)
    if (encoded === synchronized) return
    synchronized = encoded
    emitted = encoded
    emit('update:modelValue', value)
  },
  { deep: true },
)
watch(
  () => props.modelValue,
  (value) => {
    const encoded = JSON.stringify(value)
    if (encoded === emitted) return
    emitted = encoded
    const next = value.map((rule) => errorRuleDraft(rule, nextKey++))
    synchronized = JSON.stringify(next.map(errorRuleValue))
    rows.value = next
  },
  { deep: true },
)
const retries = computed(() =>
  errorRuleRetries.map((value) => ({ value, label: t('errorRules.retries.' + value) })),
)
const effects = computed(() =>
  errorRuleEffects.map((value) => ({ value, label: t('errorRules.effects.' + value) })),
)
function add(): void {
  rows.value.push(errorRuleDraft({ retry: 'none', effect: 'none' }, nextKey++, true))
}
function copy(index: number): void {
  rows.value.splice(index + 1, 0, { ...rows.value[index]!, key: nextKey++, open: true })
}
function move(index: number, offset: number): void {
  const row = rows.value.splice(index, 1)[0]!
  rows.value.splice(index + offset, 0, row)
}
function summary(index: number): string {
  const row = rows.value[index]!
  return `${row.statuses || t('errorRules.allStatuses')} · ${row.keywords.trim().replaceAll('\n', ', ') || t('errorRules.allKeywords')} → ${t('errorRules.retries.' + row.retry)} · ${t('errorRules.effects.' + row.effect)}${hasErrorRuleCooldown(row.effect) ? ` (${row.cooldown}s)` : ''}`
}
</script>

<template>
  <div class="modern-error-rules">
    <div class="modern-error-toolbar">
      <span>{{ t('errorRules.order') }}</span>
      <AppButton
        :icon="Plus"
        variant="outline"
        size="xs"
        :disabled="disabled || rows.length >= 100"
        @click="add"
        >{{ t('errorRules.add') }}</AppButton
      >
    </div>
    <p v-if="!rows.length" class="modern-error-empty">{{ t('errorRules.empty') }}</p>
    <article v-for="(row, index) in rows" :key="row.key" class="modern-error-rule">
      <header class="modern-error-heading">
        <AppButton
          variant="ghost"
          size="xs"
          :icon="row.open ? ChevronDown : ChevronRight"
          :aria-expanded="row.open"
          @click="row.open = !row.open"
          >{{ t('errorRules.rule', { number: n(index + 1) }) }}</AppButton
        >
        <AppBadge v-if="!validErrorRules([values[index]!])" tone="warning" variant="plain">{{
          t('errorRules.incomplete')
        }}</AppBadge>
        <div class="modern-error-tools">
          <AppIconButton
            :icon="ArrowUp"
            :label="t('errorRules.up')"
            size="xs"
            :disabled="disabled || index === 0"
            @click="move(index, -1)"
          />
          <AppIconButton
            :icon="ArrowDown"
            :label="t('errorRules.down')"
            size="xs"
            :disabled="disabled || index === rows.length - 1"
            @click="move(index, 1)"
          />
          <AppIconButton
            :icon="Copy"
            :label="t('errorRules.copy')"
            size="xs"
            :disabled="disabled || rows.length >= 100"
            @click="copy(index)"
          />
          <AppIconButton
            :icon="Trash2"
            :label="t('errorRules.remove')"
            size="xs"
            :disabled="disabled"
            @click="rows.splice(index, 1)"
          />
        </div>
      </header>
      <AppOverflowText v-if="!row.open" :text="summary(index)" />
      <div v-else class="modern-error-fields">
        <AppTextField
          v-model="row.statuses"
          :label="t('errorRules.statuses')"
          :placeholder="t('errorRules.statusesPlaceholder')"
          size="xs"
          :disabled="disabled"
          :error="errorRuleErrors(row).statuses ? t('errorRules.errors.statuses') : undefined"
        />
        <AppTextArea
          v-model="row.keywords"
          :label="t('errorRules.keywords')"
          :placeholder="t('errorRules.keywordsPlaceholder')"
          :rows="2"
          size="xs"
          :disabled="disabled"
        />
        <AppSelect
          v-model="row.retry"
          :label="t('errorRules.retry')"
          :options="retries"
          size="xs"
          :disabled="disabled"
        />
        <AppSelect
          v-model="row.effect"
          :label="t('errorRules.effect')"
          :options="effects"
          size="xs"
          :disabled="disabled"
        />
        <AppTextField
          v-if="hasErrorRuleCooldown(row.effect)"
          :model-value="row.cooldown"
          :label="t('errorRules.cooldown')"
          type="number"
          min="1"
          step="1"
          size="xs"
          :disabled="disabled"
          :error="errorRuleErrors(row).cooldown ? t('errorRules.errors.cooldown') : undefined"
          @update:model-value="row.cooldown = String($event)"
        />
        <AppNotice v-if="errorRuleErrors(row).condition" tone="warning">{{
          t('errorRules.errors.condition')
        }}</AppNotice>
      </div>
    </article>
    <p class="modern-error-empty">{{ t('errorRules.conditionsHelp') }}</p>
    <AppNotice
      v-if="!valid && rows.every((row) => !Object.keys(errorRuleErrors(row)).length)"
      tone="warning"
      >{{ t('errorRules.errors.invalid') }}</AppNotice
    >
  </div>
</template>

<style scoped>
.modern-error-rules,
.modern-error-rule,
.modern-error-fields {
  display: grid;
  gap: var(--modern-space-3);
}
.modern-error-toolbar,
.modern-error-heading,
.modern-error-tools {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-error-toolbar {
  flex-wrap: wrap;
}
.modern-error-tools {
  flex: none;
}
.modern-error-toolbar,
.modern-error-heading {
  justify-content: space-between;
}
.modern-error-toolbar,
.modern-error-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-error-rule {
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3);
}
</style>
