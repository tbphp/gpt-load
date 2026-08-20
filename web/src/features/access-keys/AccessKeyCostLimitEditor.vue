<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type {
  AccessKeyCostLimitRuleStatusDto,
  AccessKeyCostLimitStatusDto,
} from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import QuotaProgressBar from '@/components/ui/QuotaProgressBar.vue'
import { formatLocalInstant } from '@/lib/format'
import { quotaProgressTone } from '@/lib/quota-progress'
import { createUUID } from '@/lib/uuid'

import type { AccessKeyCostLimitRuleDraft } from './access-key-patch'

const props = defineProps<{
  modelValue: AccessKeyCostLimitRuleDraft[]
  runtimeStatus: AccessKeyCostLimitStatusDto | null
  disabled: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: AccessKeyCostLimitRuleDraft[]]
}>()
const { locale, n, t } = useI18n()

const totalCount = computed(() => props.modelValue.filter((rule) => rule.kind === 'total').length)
const periodicCount = computed(
  () => props.modelValue.filter((rule) => rule.kind === 'periodic').length,
)
const runtimeByID = computed(
  () => new Map((props.runtimeStatus?.rules ?? []).map((rule) => [rule.id, rule])),
)
const totalRules = computed(() => props.modelValue.filter((rule) => rule.kind === 'total'))
const periodicRules = computed(() => props.modelValue.filter((rule) => rule.kind === 'periodic'))

type PeriodUnit = 'seconds' | 'minutes' | 'hours' | 'days'
const periodUnits: ReadonlyArray<{ value: PeriodUnit; seconds: number }> = [
  { value: 'seconds', seconds: 1 },
  { value: 'minutes', seconds: 60 },
  { value: 'hours', seconds: 3_600 },
  { value: 'days', seconds: 86_400 },
]

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
  const limit = Number(runtime.limit_usd)
  const remaining = Number(runtime.remaining_usd)
  if (!Number.isFinite(limit) || !Number.isFinite(remaining) || limit <= 0) return 0
  return Math.round(Math.max(0, Math.min(100, (remaining / limit) * 100)))
}

function runtimeProgressValue(runtime: AccessKeyCostLimitRuleStatusDto): number | undefined {
  return runtime.status === 'inactive' ? undefined : remainingPercent(runtime)
}

function runtimeValueText(runtime: AccessKeyCostLimitRuleStatusDto): string {
  if (runtime.status === 'inactive') return t('accessKeys.costLimits.status.inactive')
  return t('accessKeys.costLimits.remainingPercent', { value: n(remainingPercent(runtime)) })
}

function runtimeWindowTooltip(runtime: AccessKeyCostLimitRuleStatusDto): string | undefined {
  if (runtime.window_started_at_ms === null || runtime.window_ends_at_ms === null) return undefined
  return t('accessKeys.costLimits.windowPeriod', {
    start: formatLocalInstant(runtime.window_started_at_ms, locale.value),
    end: formatLocalInstant(runtime.window_ends_at_ms, locale.value),
  })
}
</script>

<template>
  <div class="cost-limit-editor">
    <div class="cost-limit-editor__actions">
      <AppButton
        type="button"
        variant="secondary"
        size="compact"
        :disabled="disabled || totalCount >= 1"
        @click="addRule('total')"
      >
        <Plus :size="15" aria-hidden="true" />{{ t('accessKeys.drawer.costLimits.addTotal') }}
      </AppButton>
      <AppButton
        type="button"
        variant="secondary"
        size="compact"
        :disabled="disabled || periodicCount >= 10"
        @click="addRule('periodic')"
      >
        <Plus :size="15" aria-hidden="true" />{{ t('accessKeys.drawer.costLimits.addPeriodic') }}
      </AppButton>
      <span>{{ t('accessKeys.drawer.costLimits.ruleCount', { count: modelValue.length }) }}</span>
    </div>

    <p v-if="modelValue.length === 0" class="cost-limit-editor__empty">
      {{ t('accessKeys.drawer.costLimits.empty') }}
    </p>

    <section v-if="totalRules.length" class="cost-limit-group">
      <h4>{{ t('accessKeys.drawer.costLimits.total') }}</h4>
      <article
        v-for="rule in totalRules"
        :key="rule.clientKey"
        class="cost-limit-rule cost-limit-rule--total"
      >
        <div class="cost-limit-rule__fields">
          <label :for="`cost-limit-amount-${rule.clientKey}`">
            <span>{{ t('accessKeys.drawer.costLimits.amount') }}</span>
            <span class="cost-limit-rule__amount" data-input-shell>
              <span aria-hidden="true">$</span>
              <input
                :id="`cost-limit-amount-${rule.clientKey}`"
                data-input-inner
                :value="rule.limit_usd"
                type="text"
                inputmode="decimal"
                autocomplete="off"
                :disabled="disabled"
                @input="
                  updateRule(rule.clientKey, {
                    limit_usd: ($event.target as HTMLInputElement).value,
                  })
                "
              />
            </span>
          </label>
          <button
            type="button"
            class="cost-limit-rule__remove"
            :aria-label="t('accessKeys.drawer.costLimits.remove')"
            :disabled="disabled"
            @click="removeRule(rule.clientKey)"
          >
            <Trash2 :size="14" aria-hidden="true" />
          </button>
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
          <strong v-if="runtimeFor(rule)!.status !== 'inactive'">
            {{ runtimeValueText(runtimeFor(rule)!) }}
          </strong>
          <span v-if="runtimeFor(rule)!.status === 'exhausted'">
            {{ t('accessKeys.costLimits.notAutomatic') }}
          </span>
        </div>
      </article>
    </section>

    <section v-if="periodicRules.length" class="cost-limit-group">
      <h4>{{ t('accessKeys.drawer.costLimits.periodic') }}</h4>
      <article v-for="rule in periodicRules" :key="rule.clientKey" class="cost-limit-rule">
        <div class="cost-limit-rule__fields">
          <label :for="`cost-limit-amount-${rule.clientKey}`">
            <span>{{ t('accessKeys.drawer.costLimits.amount') }}</span>
            <span class="cost-limit-rule__amount" data-input-shell>
              <span aria-hidden="true">$</span>
              <input
                :id="`cost-limit-amount-${rule.clientKey}`"
                data-input-inner
                :value="rule.limit_usd"
                type="text"
                inputmode="decimal"
                autocomplete="off"
                :disabled="disabled"
                @input="
                  updateRule(rule.clientKey, {
                    limit_usd: ($event.target as HTMLInputElement).value,
                  })
                "
              />
            </span>
          </label>
          <label :for="`cost-limit-period-${rule.clientKey}`">
            <span>{{ t('accessKeys.drawer.costLimits.period') }}</span>
            <input
              :id="`cost-limit-period-${rule.clientKey}`"
              :value="periodValue(rule.period_seconds)"
              type="number"
              min="1"
              step="1"
              :disabled="disabled"
              @input="updatePeriodValue(rule, ($event.target as HTMLInputElement).value)"
            />
          </label>
          <label :for="`cost-limit-unit-${rule.clientKey}`">
            <span>{{ t('accessKeys.drawer.costLimits.unit') }}</span>
            <select
              :id="`cost-limit-unit-${rule.clientKey}`"
              :value="periodUnit(rule.period_seconds)"
              :disabled="disabled"
              @change="
                updatePeriodUnit(rule, ($event.target as HTMLSelectElement).value as PeriodUnit)
              "
            >
              <option v-for="unit in periodUnits" :key="unit.value" :value="unit.value">
                {{ t(`accessKeys.drawer.costLimits.units.${unit.value}`) }}
              </option>
            </select>
          </label>
          <button
            type="button"
            class="cost-limit-rule__remove"
            :aria-label="t('accessKeys.drawer.costLimits.remove')"
            :disabled="disabled"
            @click="removeRule(rule.clientKey)"
          >
            <Trash2 :size="14" aria-hidden="true" />
          </button>
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
          <strong v-if="runtimeFor(rule)!.status !== 'inactive'">
            {{ runtimeValueText(runtimeFor(rule)!) }}
          </strong>
          <AppRelativeTime
            v-if="runtimeFor(rule)!.window_ends_at_ms !== null"
            :instant="runtimeFor(rule)!.window_ends_at_ms"
            :locale="locale"
            :empty-label="t('accessKeys.costLimits.status.inactive')"
            :tooltip-content="runtimeWindowTooltip(runtimeFor(rule)!)"
            hint
          />
          <span v-else>{{ t('accessKeys.costLimits.status.inactive') }}</span>
        </div>
      </article>
    </section>
  </div>
</template>

<style scoped>
.cost-limit-editor {
  display: grid;
  gap: 9px;
}
.cost-limit-editor__actions,
.cost-limit-rule__runtime {
  display: flex;
  align-items: center;
}
.cost-limit-editor__actions {
  flex-wrap: wrap;
  gap: 6px;
}
.cost-limit-editor__actions > span,
.cost-limit-editor__empty,
.cost-limit-rule__runtime {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.cost-limit-editor__empty {
  margin: 0;
}
.cost-limit-group {
  display: grid;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 8px;
}
.cost-limit-group h4 {
  margin: 0 0 2px;
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 680;
}
.cost-limit-rule {
  display: grid;
  gap: 6px;
  padding: 7px 0;
}
.cost-limit-rule + .cost-limit-rule {
  border-top: 1px solid var(--color-border-subtle);
}
.cost-limit-rule__remove {
  display: inline-grid;
  width: 32px;
  height: 32px;
  align-self: end;
  place-items: center;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-danger);
  cursor: pointer;
}
.cost-limit-rule__remove:hover:not(:disabled) {
  background: var(--color-danger-bg);
}
.cost-limit-rule__remove:focus-visible {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
.cost-limit-rule__remove:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
.cost-limit-rule__fields {
  display: grid;
  grid-template-columns: minmax(112px, 1fr) 66px 84px 32px;
  align-items: end;
  gap: 6px;
}
.cost-limit-rule--total .cost-limit-rule__fields {
  grid-template-columns: minmax(112px, 160px) 32px;
}
.cost-limit-rule__fields label {
  display: grid;
  min-width: 0;
  gap: 3px;
}
.cost-limit-rule__fields label > span {
  color: var(--color-text-muted);
  font-size: 0.6875rem;
  font-weight: 560;
}
.cost-limit-rule__fields input,
.cost-limit-rule__fields select {
  width: 100%;
  height: 32px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 5px 7px;
  font-size: var(--text-sm);
}
.cost-limit-rule__amount {
  display: grid;
  height: 32px;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding-left: 8px;
}
.cost-limit-rule__amount input {
  height: 30px;
  border: 0;
  background: transparent;
}
.cost-limit-rule__runtime {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(54px, auto);
  min-height: 18px;
  gap: 7px;
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
  .cost-limit-rule__fields {
    grid-template-columns: minmax(92px, 1fr) 58px 76px 32px;
  }
}
</style>
