<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyCostLimitRuleDto, AccessKeyDto } from '@/api/control/types'
import { RequestCancelledError } from '@/api/errors'
import { useApiClient } from '@/api/client-context'
import { resetAccessKeyCostLimits } from '@/app/resources/access-keys'
import AppConfirmDialog from '@/components/ui/AppConfirmDialog.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import { formatUSD } from '@/lib/format'

const props = defineProps<{ accessKey: AccessKeyDto }>()
const emit = defineEmits<{ reset: [name: string] }>()
const client = useApiClient()
const { locale, t } = useI18n()
const open = ref(false)
const pending = ref(false)
const failed = ref(false)
const selectedRuleIDs = ref(new Set<number>())
const ruleList = ref<HTMLElement>()
let controller: AbortController | undefined

const selectedCount = computed(() => selectedRuleIDs.value.size)

function periodLabel(seconds: number): string {
  if (seconds % 86_400 === 0) {
    return t('accessKeys.reset.periodDays', { count: seconds / 86_400 })
  }
  if (seconds % 3_600 === 0) {
    return t('accessKeys.reset.periodHours', { count: seconds / 3_600 })
  }
  if (seconds % 60 === 0) {
    return t('accessKeys.reset.periodMinutes', { count: seconds / 60 })
  }
  return t('accessKeys.reset.periodSeconds', { count: seconds })
}

function ruleLabel(rule: AccessKeyCostLimitRuleDto): string {
  return rule.kind === 'total'
    ? t('accessKeys.reset.total')
    : t('accessKeys.reset.periodic', { period: periodLabel(rule.period_seconds) })
}

function setRuleSelected(ruleID: number, selected: boolean): void {
  const next = new Set(selectedRuleIDs.value)
  if (selected) next.add(ruleID)
  else next.delete(ruleID)
  selectedRuleIDs.value = next
}

async function focusFirstRule(): Promise<void> {
  await nextTick()
  await nextTick()
  ruleList.value?.querySelector<HTMLInputElement>('input[type="checkbox"]')?.focus()
}

function setOpen(value: boolean): void {
  if (!value && pending.value) return
  if (value) {
    selectedRuleIDs.value = new Set(props.accessKey.cost_limit_rules.map(({ id }) => id))
    failed.value = false
    open.value = true
    void focusFirstRule()
    return
  }
  controller?.abort()
  controller = undefined
  selectedRuleIDs.value = new Set()
  failed.value = false
  open.value = false
}

async function confirmReset(): Promise<void> {
  if (selectedCount.value === 0 || pending.value) return
  pending.value = true
  failed.value = false
  controller = new AbortController()
  const activeController = controller
  try {
    await resetAccessKeyCostLimits(
      client,
      props.accessKey.id,
      [...selectedRuleIDs.value],
      activeController.signal,
    )
    open.value = false
    selectedRuleIDs.value = new Set()
    emit('reset', props.accessKey.name)
  } catch (error: unknown) {
    if (!(error instanceof RequestCancelledError)) failed.value = true
  } finally {
    if (controller === activeController) controller = undefined
    pending.value = false
  }
}

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <AppConfirmDialog
    :open="open"
    :title="t('accessKeys.reset.title')"
    :description="t('accessKeys.reset.description', { name: accessKey.name })"
    :close-label="t('accessKeys.reset.close')"
    :cancel-label="t('common.cancel')"
    :confirm-label="t('accessKeys.reset.confirm', { count: selectedCount })"
    :pending="pending"
    :confirm-disabled="selectedCount === 0"
    @update:open="setOpen"
    @confirm="confirmReset"
  >
    <template #trigger>
      <slot name="trigger" :open="() => setOpen(true)" />
    </template>

    <div class="access-key-reset__body">
      <div ref="ruleList" class="access-key-reset__rules">
        <label v-for="rule in accessKey.cost_limit_rules" :key="rule.id">
          <input
            type="checkbox"
            :checked="selectedRuleIDs.has(rule.id)"
            :disabled="pending"
            @change="setRuleSelected(rule.id, ($event.target as HTMLInputElement).checked)"
          />
          <span>
            <strong>{{ ruleLabel(rule) }}</strong>
            <small>{{ formatUSD(rule.limit_usd, locale) }}</small>
          </span>
        </label>
      </div>
      <InlineFeedback tone="warning" appearance="hint">
        {{ t('accessKeys.reset.impact') }}
      </InlineFeedback>
      <InlineFeedback v-if="failed" tone="danger">{{
        t('accessKeys.reset.failed')
      }}</InlineFeedback>
    </div>
  </AppConfirmDialog>
</template>

<style scoped>
.access-key-reset__body,
.access-key-reset__rules {
  display: grid;
  gap: var(--space-2);
}
.access-key-reset__rules {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
}
.access-key-reset__rules label {
  display: grid;
  min-height: 44px;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: var(--space-3);
  background: var(--color-surface);
  padding: 7px 10px;
  cursor: pointer;
}
.access-key-reset__rules label + label {
  border-top: 1px solid var(--color-border-subtle);
}
.access-key-reset__rules label:has(input:checked) {
  background: color-mix(in srgb, var(--color-action-soft) 48%, var(--color-surface));
}
.access-key-reset__rules input {
  width: 16px;
  height: 16px;
  accent-color: var(--color-action);
}
.access-key-reset__rules label > span {
  display: flex;
  min-width: 0;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-3);
}
.access-key-reset__rules strong {
  font-size: var(--text-sm);
}
.access-key-reset__rules small {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
</style>
