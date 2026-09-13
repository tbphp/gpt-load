<script setup lang="ts">
import { DollarSign, Plus, RotateCcw, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccessKey } from '@modern/api/access-keys'
import {
  AppButton,
  AppFormSection,
  AppIconButton,
  AppSegmentedField,
  AppTextField,
} from '@modern/components/ui'
import { accessTime, accessUSD } from './access-key-display'
import { periodUnits, ruleDraft, type RuleDraft } from './access-key-draft'
const props = defineProps<{
  runtime?: AccessKey['cost_limit_status']
  disabled: boolean
  resetDisabled: boolean
  error?: string
}>()
const rules = defineModel<RuleDraft[]>({ required: true })
defineEmits<{ reset: [] }>()
const { t, locale } = useI18n()
const units = computed(() =>
  Object.keys(periodUnits).map((value) => ({ value, label: t('accessKeys.units.' + value) })),
)
const byID = computed(() => new Map(props.runtime?.rules.map((rule) => [rule.id, rule])))
function add(kind: 'total' | 'periodic'): void {
  if (
    props.disabled ||
    rules.value.filter((rule) => rule.kind === kind).length >= (kind === 'total' ? 1 : 10)
  )
    return
  rules.value = [
    ...rules.value,
    ruleDraft({ kind, limit_usd: '', ...(kind === 'periodic' ? { period_seconds: 18000 } : {}) }),
  ]
}
</script>
<template>
  <AppFormSection :title="t('accessKeys.limits')" compact>
    <template #actions
      ><AppButton
        v-if="runtime?.rules.length"
        :icon="RotateCcw"
        variant="brand"
        size="xs"
        :disabled="disabled || resetDisabled"
        @click="$emit('reset')"
        >{{ t('accessKeys.reset') }}</AppButton
      ></template
    >
    <div v-for="rule in rules" :key="rule.clientKey" class="modern-access-quota-rule">
      <div class="modern-access-quota-fields" :class="{ 'is-total': rule.kind === 'total' }">
        <span class="modern-access-quota-kind">{{
          t(rule.kind === 'total' ? 'accessKeys.totalQuota' : 'accessKeys.periodicQuota')
        }}</span>
        <AppTextField
          v-model="rule.limit_usd"
          class="modern-access-quota-amount"
          :label="t('accessKeys.amount')"
          label-hidden
          :icon="DollarSign"
          inputmode="decimal"
          size="xs"
          :disabled="disabled"
          :invalid="Boolean(error)"
          placeholder="0.00"
        />
        <div v-if="rule.kind === 'periodic'" class="modern-access-quota-period">
          <span>{{ t('accessKeys.period') }}</span>
          <AppTextField
            v-model="rule.period"
            :label="t('accessKeys.period')"
            label-hidden
            inputmode="decimal"
            size="xs"
            :disabled="disabled"
            :invalid="Boolean(error)"
          />
          <AppSegmentedField
            v-model="rule.unit"
            :label="t('accessKeys.unit')"
            label-hidden
            :options="units"
            size="xs"
            :disabled="disabled"
          />
        </div>
        <AppIconButton
          class="modern-access-quota-remove"
          :icon="Trash2"
          :label="t('accessKeys.removeRule')"
          size="xxs"
          :disabled="disabled"
          @click="rules = rules.filter((item) => item.clientKey !== rule.clientKey)"
        />
      </div>
      <div v-if="rule.id && byID.get(rule.id)" class="modern-access-quota-runtime">
        <span
          >{{ t('accessKeys.remaining') }}
          <strong>{{ accessUSD(byID.get(rule.id)!.remaining_usd, locale) }}</strong></span
        >
        <span>{{ t('accessKeys.used') }} {{ accessUSD(byID.get(rule.id)!.used_usd, locale) }}</span>
        <span>{{
          byID.get(rule.id)!.status === 'inactive'
            ? t('accessKeys.inactive')
            : byID.get(rule.id)!.window_ends_at_ms
              ? t('accessKeys.recovers', {
                  time: accessTime(byID.get(rule.id)!.window_ends_at_ms, locale),
                })
              : t('accessKeys.noRecovery')
        }}</span>
      </div>
      <p v-else-if="rule.id" class="modern-access-quota-runtime">
        {{ t('accessKeys.quotaUnavailable') }}
      </p>
    </div>
    <p v-if="!rules.length" class="modern-access-quota-empty">{{ t('accessKeys.noQuota') }}</p>
    <p v-if="error" class="modern-access-quota-error" role="alert">{{ error }}</p>
    <div class="modern-access-quota-add">
      <AppButton
        v-if="!rules.some((rule) => rule.kind === 'total')"
        :icon="Plus"
        variant="ghost"
        size="xs"
        :disabled="disabled"
        @click="add('total')"
        >{{ t('accessKeys.addTotal') }}</AppButton
      >
      <AppButton
        v-if="rules.filter((rule) => rule.kind === 'periodic').length < 10"
        :icon="Plus"
        variant="ghost"
        size="xs"
        :disabled="disabled"
        @click="add('periodic')"
        >{{ t('accessKeys.addPeriod') }}</AppButton
      >
    </div>
  </AppFormSection>
</template>
<style scoped>
.modern-access-quota-rule {
  container: modern-access-quota / inline-size;
  min-width: 0;
  padding: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
}
.modern-access-quota-fields {
  display: grid;
  grid-template-areas: 'kind amount period remove';
  grid-template-columns: max-content minmax(80px, 1fr) max-content auto;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-access-quota-fields.is-total {
  grid-template-areas: 'kind amount remove';
  grid-template-columns: max-content minmax(0, 1fr) auto;
}
.modern-access-quota-kind {
  grid-area: kind;
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-access-quota-amount {
  grid-area: amount;
}
.modern-access-quota-period {
  grid-area: period;
  display: grid;
  grid-template-columns: max-content 52px max-content;
  align-items: center;
  gap: var(--modern-space-1-5);
  justify-self: start;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-access-quota-remove {
  grid-area: remove;
}
.modern-access-quota-runtime {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-1) var(--modern-space-3);
  margin-top: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-access-quota-runtime strong {
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
.modern-access-quota-add {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-access-quota-empty {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-access-quota-error {
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
}
@container modern-access-quota (max-width: 440px) {
  .modern-access-quota-fields:not(.is-total) {
    grid-template-areas: 'kind amount remove' 'period period period';
    grid-template-columns: max-content minmax(0, 1fr) auto;
  }
}
</style>
