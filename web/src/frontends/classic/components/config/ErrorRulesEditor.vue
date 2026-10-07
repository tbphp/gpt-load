<script setup lang="ts">
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, Copy, Plus, Trash2 } from '@lucide/vue'
import { computed, ref, useId, watch } from 'vue'
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
import AppButton from '@/components/ui/AppButton.vue'
import IconButton from '@/components/ui/IconButton.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import FormField from '@/components/ui/FormField.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

const props = defineProps<{ modelValue: ErrorRule[]; disabled?: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: ErrorRule[]]
  'update:valid': [value: boolean]
}>()
const { t, n } = useI18n()
const instanceId = useId()
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
  <div class="error-rules">
    <div class="error-rules__toolbar">
      <span>{{ t('errorRules.order') }}</span>
      <AppButton size="compact" :disabled="disabled || rows.length >= 100" @click="add"
        ><Plus :size="14" aria-hidden="true" />{{ t('errorRules.add') }}</AppButton
      >
    </div>
    <p v-if="!rows.length" class="error-rules__note">{{ t('errorRules.empty') }}</p>
    <article v-for="(row, index) in rows" :key="row.key" class="error-rule">
      <header class="error-rules__heading">
        <AppButton
          size="compact"
          variant="ghost"
          :aria-expanded="row.open"
          @click="row.open = !row.open"
          ><component :is="row.open ? ChevronDown : ChevronRight" :size="14" aria-hidden="true" />{{
            t('errorRules.rule', { number: n(index + 1) })
          }}</AppButton
        >
        <StatusBadge v-if="!validErrorRules([values[index]!])" tone="warning" size="compact">{{
          t('errorRules.incomplete')
        }}</StatusBadge>
        <div class="error-rules__tools">
          <IconButton
            :label="t('errorRules.up')"
            size="compact"
            :disabled="disabled || index === 0"
            @click="move(index, -1)"
            ><ArrowUp :size="14" aria-hidden="true"
          /></IconButton>
          <IconButton
            :label="t('errorRules.down')"
            size="compact"
            :disabled="disabled || index === rows.length - 1"
            @click="move(index, 1)"
            ><ArrowDown :size="14" aria-hidden="true"
          /></IconButton>
          <IconButton
            :label="t('errorRules.copy')"
            size="compact"
            :disabled="disabled || rows.length >= 100"
            @click="copy(index)"
            ><Copy :size="14" aria-hidden="true"
          /></IconButton>
          <IconButton
            :label="t('errorRules.remove')"
            size="compact"
            :disabled="disabled"
            @click="rows.splice(index, 1)"
            ><Trash2 :size="14" aria-hidden="true"
          /></IconButton>
        </div>
      </header>
      <OverflowTooltip v-if="!row.open" class="error-rules__summary" :content="summary(index)">{{
        summary(index)
      }}</OverflowTooltip>
      <div v-else class="error-rules__fields">
        <FormField
          :id="`${instanceId}-${row.key}-statuses`"
          :label="t('errorRules.statuses')"
          size="compact"
          :error="errorRuleErrors(row).statuses ? t('errorRules.errors.statuses') : undefined"
        >
          <template #default="{ invalid, describedBy }"
            ><AppTextInput
              :id="`${instanceId}-${row.key}-statuses`"
              v-model="row.statuses"
              :label="t('errorRules.statuses')"
              :placeholder="t('errorRules.statusesPlaceholder')"
              size="compact"
              :disabled="disabled"
              :invalid="invalid"
              :described-by="describedBy"
          /></template>
        </FormField>
        <FormField
          :id="`${instanceId}-${row.key}-keywords`"
          :label="t('errorRules.keywords')"
          size="compact"
        >
          <textarea
            :id="`${instanceId}-${row.key}-keywords`"
            v-model="row.keywords"
            class="error-rules__textarea"
            :placeholder="t('errorRules.keywordsPlaceholder')"
            rows="2"
            :disabled="disabled"
          />
        </FormField>
        <FormField
          :id="`${instanceId}-${row.key}-retry`"
          :label="t('errorRules.retry')"
          size="compact"
          ><AppSelect
            :id="`${instanceId}-${row.key}-retry`"
            v-model="row.retry"
            :label="t('errorRules.retry')"
            :options="retries"
            size="compact"
            :disabled="disabled"
        /></FormField>
        <FormField
          :id="`${instanceId}-${row.key}-effect`"
          :label="t('errorRules.effect')"
          size="compact"
          ><AppSelect
            :id="`${instanceId}-${row.key}-effect`"
            v-model="row.effect"
            :label="t('errorRules.effect')"
            :options="effects"
            size="compact"
            :disabled="disabled"
        /></FormField>
        <FormField
          v-if="hasErrorRuleCooldown(row.effect)"
          :id="`${instanceId}-${row.key}-cooldown`"
          :label="t('errorRules.cooldown')"
          size="compact"
          :error="errorRuleErrors(row).cooldown ? t('errorRules.errors.cooldown') : undefined"
        >
          <template #default="{ invalid, describedBy }"
            ><AppTextInput
              :id="`${instanceId}-${row.key}-cooldown`"
              v-model="row.cooldown"
              :label="t('errorRules.cooldown')"
              type="number"
              min="1"
              step="1"
              size="compact"
              :disabled="disabled"
              :invalid="invalid"
              :described-by="describedBy"
          /></template>
        </FormField>
        <InlineFeedback v-if="errorRuleErrors(row).condition" tone="warning">{{
          t('errorRules.errors.condition')
        }}</InlineFeedback>
      </div>
    </article>
    <p class="error-rules__note">{{ t('errorRules.conditionsHelp') }}</p>
    <InlineFeedback
      v-if="!valid && rows.every((row) => !Object.keys(errorRuleErrors(row)).length)"
      tone="warning"
      >{{ t('errorRules.errors.invalid') }}</InlineFeedback
    >
  </div>
</template>

<style scoped>
.error-rules,
.error-rule,
.error-rules__fields {
  display: grid;
  gap: var(--space-3);
}
.error-rules__toolbar,
.error-rules__heading,
.error-rules__tools {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}
.error-rules__toolbar,
.error-rules__heading {
  justify-content: space-between;
  flex-wrap: wrap;
}
.error-rules__tools {
  flex: none;
}
.error-rules__toolbar,
.error-rules__note,
.error-rules__summary {
  color: var(--color-text-muted);
  font-size: var(--text-meta);
}
.error-rules__summary {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.error-rule {
  min-width: 0;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  padding: var(--space-3);
}
.error-rules__textarea {
  width: 100%;
  min-width: 0;
  resize: vertical;
  padding: var(--space-2);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  font: inherit;
}
</style>
