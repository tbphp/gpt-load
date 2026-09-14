<script setup lang="ts">
import { numberFormatter } from '@modern/components/ui/intl-formatters'
import { RotateCcw } from '@lucide/vue'
import { useQueryClient } from '@tanstack/vue-query'
import { nextTick, onScopeDispose, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  accessDetailKey,
  accessKeysKey,
  resetAccessQuota,
  type AccessKey,
  type CostRule,
} from '@modern/api/access-keys'
import { useMessages } from '@modern/app/messages'
import { AppCheckbox, AppConfirmDialog } from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { periodUnits } from './access-key-draft'

const props = defineProps<{ row: AccessKey }>()
const emit = defineEmits<{ close: []; reset: []; pending: [value: boolean] }>()
const { t, locale } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const messages = useMessages()
const controller = new AbortController()
const rules = props.row.cost_limit_rules.filter(
  (rule): rule is CostRule & { id: number } => rule.id !== undefined,
)
const selected = ref(rules.map((rule) => rule.id))
const pending = ref(false)
const error = ref('')

function select(id: number, checked: boolean): void {
  selected.value = checked
    ? [...selected.value, id]
    : selected.value.filter((value) => value !== id)
}
function ruleLabel(rule: CostRule): string {
  if (rule.kind === 'total') return t('accessKeys.totalQuota')
  const seconds = rule.period_seconds!
  const unit = seconds % periodUnits.day === 0 ? 'day' : 'hour'
  return t('accessKeys.every', {
    count: numberFormatter(locale.value, { maximumFractionDigits: 12 }).format(
      seconds / periodUnits[unit],
    ),
    unit: t('accessKeys.units.' + unit),
  })
}
async function reset(): Promise<void> {
  if (pending.value || !selected.value.length) return
  pending.value = true
  emit('pending', true)
  error.value = ''
  let completed = false
  try {
    await resetAccessQuota(client, props.row.id, [...selected.value], controller.signal)
    if (controller.signal.aborted) return
    completed = true
    await Promise.allSettled([
      cache.invalidateQueries({ queryKey: accessDetailKey(props.row.id) }),
      cache.invalidateQueries({ queryKey: accessKeysKey }),
    ])
  } catch {
    if (!controller.signal.aborted) error.value = t('accessKeys.resetUnknown')
  } finally {
    pending.value = false
    emit('pending', false)
  }
  if (!completed || controller.signal.aborted) return
  await nextTick()
  emit('reset')
  messages.show({ tone: 'success', text: t('accessKeys.resetDone') })
}
useLoadingActivity(pending)
onScopeDispose(() => {
  controller.abort()
  emit('pending', false)
})
</script>

<template>
  <AppConfirmDialog
    :open="true"
    :icon="RotateCcw"
    :title="t('accessKeys.resetTitle')"
    :subject="row.name"
    :description="t('accessKeys.resetHelp')"
    :confirm-label="t('accessKeys.reset')"
    :disabled="!selected.length"
    :pending="pending"
    :error="error"
    @cancel="emit('close')"
    @confirm="reset"
  >
    <div class="modern-access-reset-list" :aria-label="t('accessKeys.resetSelect')">
      <AppCheckbox
        v-for="rule in rules"
        :key="rule.id"
        :model-value="selected.includes(rule.id)"
        :label="ruleLabel(rule)"
        :disabled="pending"
        @update:model-value="select(rule.id, $event)"
      />
    </div>
  </AppConfirmDialog>
</template>

<style scoped>
.modern-access-reset-list {
  display: grid;
  gap: var(--modern-space-3);
}
</style>
