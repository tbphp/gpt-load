<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyCostLimitStatusDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'

import type { AccessKeyCostLimitRuleDraft } from './access-key-patch'

const props = defineProps<{
  modelValue: AccessKeyCostLimitRuleDraft[]
  runtimeStatus: AccessKeyCostLimitStatusDto | null
  disabled: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: AccessKeyCostLimitRuleDraft[]]
}>()
const { locale, t } = useI18n()

const totalCount = computed(() => props.modelValue.filter((rule) => rule.kind === 'total').length)
const periodicCount = computed(
  () => props.modelValue.filter((rule) => rule.kind === 'periodic').length,
)
const runtimeByID = computed(
  () => new Map((props.runtimeStatus?.rules ?? []).map((rule) => [rule.id, rule])),
)

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
      clientKey: crypto.randomUUID(),
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

function runtimeTone(status: 'available' | 'inactive' | 'exhausted') {
  if (status === 'exhausted') return 'danger' as const
  if (status === 'inactive') return 'neutral' as const
  return 'success' as const
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

    <article v-for="rule in modelValue" :key="rule.clientKey" class="cost-limit-rule">
      <header>
        <div>
          <strong>{{
            t(
              rule.kind === 'total'
                ? 'accessKeys.drawer.costLimits.total'
                : 'accessKeys.drawer.costLimits.periodic',
            )
          }}</strong>
          <StatusBadge
            v-if="rule.id !== undefined && runtimeByID.get(rule.id)"
            :tone="runtimeTone(runtimeByID.get(rule.id)!.status)"
            size="compact"
          >
            {{ t(`accessKeys.costLimits.status.${runtimeByID.get(rule.id)!.status}`) }}
          </StatusBadge>
        </div>
        <button
          type="button"
          class="cost-limit-rule__remove"
          :aria-label="t('accessKeys.drawer.costLimits.remove')"
          :disabled="disabled"
          @click="removeRule(rule.clientKey)"
        >
          <Trash2 :size="15" aria-hidden="true" />
        </button>
      </header>

      <div class="cost-limit-rule__fields">
        <label :for="`cost-limit-amount-${rule.clientKey}`">
          <span>{{ t('accessKeys.drawer.costLimits.amount') }}</span>
          <div class="cost-limit-rule__amount">
            <span aria-hidden="true">$</span>
            <input
              :id="`cost-limit-amount-${rule.clientKey}`"
              :value="rule.limit_usd"
              type="text"
              inputmode="decimal"
              autocomplete="off"
              :disabled="disabled"
              @input="
                updateRule(rule.clientKey, { limit_usd: ($event.target as HTMLInputElement).value })
              "
            />
          </div>
        </label>

        <template v-if="rule.kind === 'periodic'">
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
        </template>
      </div>

      <div
        v-if="rule.id !== undefined && runtimeByID.get(rule.id)"
        class="cost-limit-rule__runtime"
      >
        <span>
          {{
            t('accessKeys.drawer.costLimits.used', {
              used: runtimeByID.get(rule.id)!.used_usd,
              limit: runtimeByID.get(rule.id)!.limit_usd,
            })
          }}
        </span>
        <span v-if="runtimeByID.get(rule.id)!.window_ends_at_ms !== null">
          {{
            t(
              runtimeByID.get(rule.id)!.status === 'exhausted'
                ? 'accessKeys.drawer.costLimits.availableAgain'
                : 'accessKeys.drawer.costLimits.windowEnds',
            )
          }}
          <AppRelativeTime
            :instant="runtimeByID.get(rule.id)!.window_ends_at_ms"
            :locale="locale"
            :empty-label="t('accessKeys.costLimits.notAutomatic')"
            hint
          />
        </span>
        <span v-else-if="rule.kind === 'total' && runtimeByID.get(rule.id)!.status === 'exhausted'">
          {{ t('accessKeys.costLimits.notAutomatic') }}
        </span>
      </div>
    </article>

    <p class="cost-limit-editor__note">{{ t('accessKeys.drawer.costLimits.note') }}</p>
  </div>
</template>

<style scoped>
.cost-limit-editor {
  display: grid;
  gap: var(--space-3);
}
.cost-limit-editor__actions,
.cost-limit-rule header,
.cost-limit-rule header > div,
.cost-limit-rule__runtime {
  display: flex;
  align-items: center;
}
.cost-limit-editor__actions {
  flex-wrap: wrap;
  gap: var(--space-2);
}
.cost-limit-editor__actions > span,
.cost-limit-editor__empty,
.cost-limit-editor__note,
.cost-limit-rule__runtime {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
.cost-limit-editor__empty,
.cost-limit-editor__note {
  margin: 0;
}
.cost-limit-rule {
  display: grid;
  gap: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface-sunken);
  padding: var(--space-3);
}
.cost-limit-rule header {
  justify-content: space-between;
  gap: var(--space-3);
}
.cost-limit-rule header > div {
  flex-wrap: wrap;
  gap: var(--space-2);
}
.cost-limit-rule__remove {
  display: inline-grid;
  width: 36px;
  height: 36px;
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
  grid-template-columns: minmax(0, 1.3fr) minmax(92px, 0.7fr) minmax(116px, 0.8fr);
  gap: var(--space-3);
}
.cost-limit-rule__fields label {
  display: grid;
  gap: 6px;
}
.cost-limit-rule__fields label > span {
  color: var(--color-text-muted);
  font-size: var(--text-label-xs);
  font-weight: 560;
}
.cost-limit-rule__fields input,
.cost-limit-rule__fields select {
  width: 100%;
  height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
}
.cost-limit-rule__amount {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding-left: var(--space-3);
}
.cost-limit-rule__amount input {
  border: 0;
  background: transparent;
}
.cost-limit-rule__runtime {
  flex-wrap: wrap;
  gap: var(--space-3);
}
@media (max-width: 560px) {
  .cost-limit-rule__fields {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
