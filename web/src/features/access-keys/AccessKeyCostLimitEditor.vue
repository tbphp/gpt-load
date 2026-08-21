<script setup lang="ts">
import { Plus, X } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  AccessKeyCostLimitRuleStatusDto,
  AccessKeyCostLimitStatusDto,
} from '@/api/control/types'
import AppSwitch from '@/components/ui/AppSwitch.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import IconButton from '@/components/ui/IconButton.vue'
import QuotaProgressBar from '@/components/ui/QuotaProgressBar.vue'
import { quotaProgressTone } from '@/lib/quota-progress'
import { createUUID } from '@/lib/uuid'

import AccessKeyCostLimitWindowTime from './AccessKeyCostLimitWindowTime.vue'
import type { AccessKeyCostLimitRuleDraft } from './access-key-patch'

const props = defineProps<{
  modelValue: AccessKeyCostLimitRuleDraft[]
  runtimeStatus: AccessKeyCostLimitStatusDto | null
  disabled: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: AccessKeyCostLimitRuleDraft[]]
}>()
const { n, t } = useI18n()

const totalCount = computed(() => props.modelValue.filter((rule) => rule.kind === 'total').length)
const periodicCount = computed(
  () => props.modelValue.filter((rule) => rule.kind === 'periodic').length,
)
const runtimeByID = computed(
  () => new Map((props.runtimeStatus?.rules ?? []).map((rule) => [rule.id, rule])),
)
const totalRules = computed(() => props.modelValue.filter((rule) => rule.kind === 'total'))
const periodicRules = computed(() => props.modelValue.filter((rule) => rule.kind === 'periodic'))
const totalEnabled = computed({
  get: () => totalCount.value > 0,
  set: (enabled: boolean) => {
    if (props.disabled) return
    if (enabled) {
      if (totalCount.value === 0) addRule('total')
      return
    }
    removeRules('total')
  },
})
const periodicEnabled = computed({
  get: () => periodicCount.value > 0,
  set: (enabled: boolean) => {
    if (props.disabled) return
    if (enabled) {
      if (periodicCount.value === 0) addRule('periodic')
      return
    }
    removeRules('periodic')
  },
})

type PeriodUnit = 'seconds' | 'minutes' | 'hours' | 'days'
const periodUnits: ReadonlyArray<{ value: PeriodUnit; seconds: number }> = [
  { value: 'seconds', seconds: 1 },
  { value: 'minutes', seconds: 60 },
  { value: 'hours', seconds: 3_600 },
  { value: 'days', seconds: 86_400 },
]
const periodUnitOptions = computed(() =>
  periodUnits.map((unit) => ({
    value: unit.value,
    label: t(`accessKeys.drawer.costLimits.units.${unit.value}`),
  })),
)

function updateRule(clientKey: string, patch: Partial<AccessKeyCostLimitRuleDraft>): void {
  emit(
    'update:modelValue',
    props.modelValue.map((rule) => (rule.clientKey === clientKey ? { ...rule, ...patch } : rule)),
  )
}

function removeRule(clientKey: string): void {
  emit(
    'update:modelValue',
    props.modelValue.filter((rule) => rule.clientKey !== clientKey),
  )
}

function removeRules(kind: 'total' | 'periodic'): void {
  emit(
    'update:modelValue',
    props.modelValue.filter((rule) => rule.kind !== kind),
  )
}

function addRule(kind: 'total' | 'periodic'): void {
  if (props.disabled || (kind === 'total' ? totalCount.value >= 1 : periodicCount.value >= 10)) {
    return
  }
  emit('update:modelValue', [
    ...props.modelValue,
    {
      clientKey: createUUID(),
      kind,
      limit_usd: kind === 'total' ? '100' : '20',
      ...(kind === 'periodic' ? { period_seconds: 18_000 } : {}),
    },
  ])
}

function periodUnit(seconds: number | undefined): PeriodUnit {
  const value = seconds ?? 0
  for (const unit of [...periodUnits].reverse()) {
    if (value > 0 && value % unit.seconds === 0) return unit.value
  }
  return 'seconds'
}

function unitSeconds(unit: PeriodUnit): number {
  return periodUnits.find((candidate) => candidate.value === unit)?.seconds ?? 1
}

function periodValue(seconds: number | undefined): number {
  const unit = periodUnit(seconds)
  return Math.max(1, Math.floor((seconds ?? 0) / unitSeconds(unit)))
}

function updatePeriodValue(rule: AccessKeyCostLimitRuleDraft, raw: string): void {
  const value = Number(raw)
  updateRule(rule.clientKey, {
    period_seconds: Number.isFinite(value)
      ? value * unitSeconds(periodUnit(rule.period_seconds))
      : 0,
  })
}

function updatePeriodUnit(rule: AccessKeyCostLimitRuleDraft, unit: PeriodUnit): void {
  updateRule(rule.clientKey, {
    period_seconds: periodValue(rule.period_seconds) * unitSeconds(unit),
  })
}

function runtimeFor(
  rule: AccessKeyCostLimitRuleDraft,
): AccessKeyCostLimitRuleStatusDto | undefined {
  return rule.id === undefined ? undefined : runtimeByID.value.get(rule.id)
}

function remainingPercent(runtime: AccessKeyCostLimitRuleStatusDto): number {
  if (runtime.status === 'inactive') return 100
  const limit = Number(runtime.limit_usd)
  const remaining = Number(runtime.remaining_usd)
  if (!Number.isFinite(limit) || !Number.isFinite(remaining) || limit <= 0) return 0
  return Math.round(Math.max(0, Math.min(100, (remaining / limit) * 100)))
}

function runtimeProgressValue(runtime: AccessKeyCostLimitRuleStatusDto): number {
  return remainingPercent(runtime)
}

function runtimeValueText(runtime: AccessKeyCostLimitRuleStatusDto): string {
  return t('accessKeys.costLimits.remainingPercent', { value: n(remainingPercent(runtime)) })
}
</script>

<template>
  <div class="cost-limit-editor">
    <section class="cost-limit-section">
      <header class="cost-limit-section__header">
        <div class="cost-limit-section__intro">
          <h3>{{ t('accessKeys.drawer.costLimits.total') }}</h3>
          <p>{{ t('accessKeys.drawer.costLimits.totalDescription') }}</p>
        </div>
        <div class="cost-limit-section__toggle">
          <AppSwitch
            v-model="totalEnabled"
            :label="t('accessKeys.drawer.costLimits.enableTotal')"
            :disabled="disabled"
          />
        </div>
      </header>

      <div v-if="totalEnabled" class="cost-limit-section__content">
        <div class="cost-limit-fields-header cost-limit-fields-header--total">
          <span>{{ t('accessKeys.drawer.costLimits.amount') }}</span>
        </div>
        <article v-for="rule in totalRules" :key="rule.clientKey" class="cost-limit-rule">
          <div
            class="cost-limit-input-group cost-limit-input-group--total"
            role="group"
            :aria-label="t('accessKeys.drawer.costLimits.total')"
          >
            <AppTextInput
              :id="`cost-limit-amount-${rule.clientKey}`"
              v-model="rule.limit_usd"
              :label="t('accessKeys.drawer.costLimits.amount')"
              appearance="surface"
              size="compact"
              monospace
              inputmode="decimal"
              :disabled="disabled"
            >
              <template #leading>$</template>
            </AppTextInput>
          </div>
          <div v-if="runtimeFor(rule)" class="cost-limit-rule__runtime">
            <QuotaProgressBar
              :value="runtimeProgressValue(runtimeFor(rule)!)"
              :tone="
                quotaProgressTone(
                  remainingPercent(runtimeFor(rule)!),
                  runtimeFor(rule)!.status === 'exhausted',
                )
              "
              :label="t('accessKeys.drawer.costLimits.total')"
              :value-text="runtimeValueText(runtimeFor(rule)!)"
              compact
            />
            <strong>{{ runtimeValueText(runtimeFor(rule)!) }}</strong>
            <span v-if="runtimeFor(rule)!.status === 'exhausted'">
              {{ t('accessKeys.costLimits.notAutomatic') }}
            </span>
          </div>
        </article>
      </div>
    </section>

    <section class="cost-limit-section">
      <header class="cost-limit-section__header">
        <div class="cost-limit-section__intro">
          <h3>{{ t('accessKeys.drawer.costLimits.periodic') }}</h3>
          <p>{{ t('accessKeys.drawer.costLimits.periodicDescription') }}</p>
        </div>
        <div class="cost-limit-section__toggle">
          <AppSwitch
            v-model="periodicEnabled"
            :label="t('accessKeys.drawer.costLimits.enablePeriodic')"
            :disabled="disabled"
          />
        </div>
      </header>

      <div v-if="periodicEnabled" class="cost-limit-section__content">
        <div class="cost-limit-fields-header cost-limit-fields-header--periodic">
          <span>{{ t('accessKeys.drawer.costLimits.amount') }}</span>
          <span>{{ t('accessKeys.drawer.costLimits.period') }}</span>
          <span>{{ t('accessKeys.drawer.costLimits.unit') }}</span>
          <span aria-hidden="true"></span>
        </div>
        <div class="cost-limit-rule-list">
          <article
            v-for="(rule, index) in periodicRules"
            :key="rule.clientKey"
            class="cost-limit-rule"
          >
            <div class="cost-limit-rule__row">
              <div
                class="cost-limit-input-group cost-limit-input-group--periodic"
                role="group"
                :aria-label="t('accessKeys.drawer.costLimits.periodic')"
              >
                <AppTextInput
                  :id="`cost-limit-amount-${rule.clientKey}`"
                  v-model="rule.limit_usd"
                  :label="t('accessKeys.drawer.costLimits.amount')"
                  appearance="surface"
                  size="compact"
                  monospace
                  inputmode="decimal"
                  :disabled="disabled"
                >
                  <template #leading>$</template>
                </AppTextInput>
                <AppTextInput
                  :id="`cost-limit-period-${rule.clientKey}`"
                  :model-value="String(periodValue(rule.period_seconds))"
                  :label="t('accessKeys.drawer.costLimits.period')"
                  type="number"
                  appearance="surface"
                  size="compact"
                  monospace
                  inputmode="numeric"
                  :disabled="disabled"
                  @update:model-value="updatePeriodValue(rule, $event)"
                />
                <AppSelect
                  :model-value="periodUnit(rule.period_seconds)"
                  :label="t('accessKeys.drawer.costLimits.unit')"
                  :options="periodUnitOptions"
                  variant="embedded"
                  size="compact"
                  :disabled="disabled"
                  @update:model-value="updatePeriodUnit(rule, $event as PeriodUnit)"
                />
              </div>
              <div class="cost-limit-rule__actions">
                <IconButton
                  v-if="index > 0"
                  class="cost-limit-rule__remove"
                  variant="ghost"
                  size="xs"
                  :label="t('accessKeys.drawer.costLimits.remove')"
                  :disabled="disabled"
                  @click="removeRule(rule.clientKey)"
                >
                  <X :size="14" aria-hidden="true" />
                </IconButton>
                <span v-else aria-hidden="true"></span>
                <IconButton
                  v-if="index === periodicRules.length - 1"
                  class="cost-limit-rule__add"
                  variant="ghost"
                  size="xs"
                  :label="t('accessKeys.drawer.costLimits.addPeriodic')"
                  :disabled="disabled || periodicCount >= 10"
                  @click="addRule('periodic')"
                >
                  <Plus :size="14" aria-hidden="true" />
                </IconButton>
                <span v-else aria-hidden="true"></span>
              </div>
            </div>
            <div v-if="runtimeFor(rule)" class="cost-limit-rule__runtime">
              <QuotaProgressBar
                :value="runtimeProgressValue(runtimeFor(rule)!)"
                :tone="
                  quotaProgressTone(
                    remainingPercent(runtimeFor(rule)!),
                    runtimeFor(rule)!.status === 'exhausted',
                  )
                "
                :label="t('accessKeys.drawer.costLimits.periodic')"
                :value-text="runtimeValueText(runtimeFor(rule)!)"
                compact
              />
              <strong>{{ runtimeValueText(runtimeFor(rule)!) }}</strong>
              <AccessKeyCostLimitWindowTime :rule="runtimeFor(rule)!" />
            </div>
          </article>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.cost-limit-editor {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
}
.cost-limit-section {
  display: grid;
  min-width: 0;
  gap: var(--space-3);
}
.cost-limit-section + .cost-limit-section {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-4);
}
.cost-limit-section__header,
.cost-limit-section__toggle {
  display: flex;
  align-items: center;
}
.cost-limit-section__header {
  justify-content: space-between;
  gap: var(--space-3);
}
.cost-limit-section__intro {
  min-width: 0;
}
.cost-limit-section__intro h3 {
  margin: 0;
  color: var(--color-text);
  font-size: var(--text-body-sm);
  font-weight: 700;
}
.cost-limit-section__intro p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.45;
}
.cost-limit-section__toggle {
  flex: 0 0 auto;
}
.cost-limit-section__content,
.cost-limit-rule-list {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}
.cost-limit-rule {
  display: grid;
  min-width: 0;
  gap: var(--space-2);
}
.cost-limit-rule__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 56px;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}
.cost-limit-rule__actions {
  display: grid;
  width: 56px;
  grid-auto-flow: column;
  grid-auto-columns: 28px;
  align-items: center;
}
.cost-limit-rule__remove,
.cost-limit-rule__add {
  color: var(--color-text-faint);
}
.cost-limit-rule__remove:hover:not(:disabled) {
  color: var(--color-danger);
}
.cost-limit-rule__add:hover:not(:disabled) {
  color: var(--color-action);
}
.cost-limit-fields-header {
  display: grid;
  min-width: 0;
  align-items: end;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.cost-limit-fields-header--total {
  grid-template-columns: minmax(0, 1fr);
}
.cost-limit-fields-header--periodic {
  grid-template-columns: minmax(0, 1fr) 76px 96px 64px;
}
.cost-limit-input-group {
  display: grid;
  width: 100%;
  min-width: 0;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
.cost-limit-input-group--total {
  grid-template-columns: minmax(0, 1fr);
}
.cost-limit-input-group--periodic {
  grid-template-columns: minmax(0, 1fr) 76px 96px;
}
.cost-limit-input-group:focus-within {
  border-color: var(--color-focus);
  outline: 0;
  box-shadow: var(--focus-ring);
}
.cost-limit-input-group :deep(.app-text-input) {
  border: 0;
  border-left: 1px solid var(--color-border-subtle);
  border-radius: 0;
  background: none;
  padding-left: var(--space-2);
}
.cost-limit-input-group :deep(.app-text-input:first-child) {
  border-left: 0;
}
.cost-limit-input-group :deep(.app-text-input[data-input-shell]:focus-within) {
  border-color: var(--color-border-subtle);
  outline: 0;
  box-shadow: none;
}
.cost-limit-input-group :deep(.app-select__trigger) {
  align-self: stretch;
  height: 100%;
  min-height: var(--control-xs);
  border-left: 1px solid var(--color-border-subtle);
  background: transparent;
  font-size: var(--text-meta);
}
.cost-limit-rule__runtime {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(54px, auto);
  min-height: 18px;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.cost-limit-rule__runtime strong {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  font-weight: 650;
  white-space: nowrap;
}
.cost-limit-rule__runtime > :last-child {
  min-width: 0;
  overflow: hidden;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 560px) {
  .cost-limit-section__header {
    align-items: flex-start;
  }

  .cost-limit-fields-header--periodic {
    grid-template-columns: minmax(0, 1fr) 58px 76px 64px;
  }

  .cost-limit-input-group--periodic {
    grid-template-columns: minmax(0, 1fr) 58px 76px;
  }
}
</style>
