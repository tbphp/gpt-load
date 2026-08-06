<script setup lang="ts">
import { ListFilter, X } from '@lucide/vue'
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyOptionDto, GroupOptionDto } from '@/api/control/types'
import AppDateTimeRangePicker from '@/components/ui/AppDateTimeRangePicker.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'

import type { LogFilterDraft, LogFilterErrors } from './log-filters'
import LogsAdvancedFilterDrawer from './LogsAdvancedFilterDrawer.vue'

interface AppliedChip {
  key: string
  label: string
}

const props = defineProps<{
  draft: LogFilterDraft
  errors: LogFilterErrors
  groups: GroupOptionDto[]
  accessKeys: AccessKeyOptionDto[]
  groupsFailed: boolean
  accessKeysFailed: boolean
  appliedChips: AppliedChip[]
  advancedCount: number
}>()
const emit = defineEmits<{
  updateField: [field: keyof LogFilterDraft, value: string]
  removeFilter: [key: string]
  apply: []
  reset: []
}>()
const { t } = useI18n()
const advancedOpen = ref(false)

const groupOptions = computed(() => [
  { value: '', label: t('monitor.logs.filters.anyGroup') },
  ...props.groups.map((group) => ({
    value: String(group.id),
    label: `${group.name} · #${group.id}`,
  })),
])
const statusOptions = computed(() => [
  { value: '', label: t('monitor.logs.filters.anyStatus') },
  ...(['success', 'error', 'incomplete', 'canceled'] as const).map((value) => ({
    value,
    label: t(`monitor.logs.status.${value}`),
  })),
])
const firstError = computed(() => {
  const key = Object.values(props.errors)[0]
  return key ? t(key) : ''
})

function update(field: keyof LogFilterDraft, value: string): void {
  emit('updateField', field, value)
}

function submit(): void {
  emit('apply')
}

function reset(): void {
  advancedOpen.value = false
  emit('reset')
}

async function applyAdvanced(): Promise<void> {
  emit('apply')
  await nextTick()
  if (Object.keys(props.errors).length === 0) advancedOpen.value = false
}
</script>

<template>
  <form class="logs-filter" :aria-label="t('monitor.logs.filters.label')" @submit.prevent="submit">
    <div v-if="appliedChips.length" class="logs-filter__chips">
      <span class="logs-filter__chips-label">{{ t('monitor.logs.filters.applied') }}</span>
      <template v-for="chip in appliedChips" :key="chip.key">
        <span v-if="chip.key === 'time'" class="logs-filter__chip logs-filter__chip--fixed">
          {{ chip.label }}
        </span>
        <button
          v-else
          type="button"
          class="logs-filter__chip"
          :aria-label="t('monitor.logs.filters.remove', { value: chip.label })"
          @click="emit('removeFilter', chip.key)"
        >
          <span>{{ chip.label }}</span>
          <X :size="12" aria-hidden="true" />
        </button>
      </template>
    </div>

    <div class="logs-filter__row">
      <AppDateTimeRangePicker
        :from="draft.from"
        :to="draft.to"
        :label="t('monitor.logs.filters.timeRange')"
        :from-label="t('monitor.logs.filters.from')"
        :to-label="t('monitor.logs.filters.to')"
        :timezone-label="t('monitor.logs.filters.timezone')"
        :from-error="errors.from ? t(errors.from) : undefined"
        :to-error="errors.to ? t(errors.to) : undefined"
        @update:from="update('from', $event)"
        @update:to="update('to', $event)"
      />

      <AppSelect
        class="logs-filter__group"
        :model-value="draft.group_id"
        :label="t('monitor.logs.filters.group')"
        :options="groupOptions"
        size="compact"
        :disabled="groupsFailed"
        @update:model-value="update('group_id', $event)"
      />
      <span class="logs-filter__model">
        <AppTextInput
          :model-value="draft.client_model"
          :label="t('monitor.logs.filters.clientModel')"
          :placeholder="t('monitor.logs.filters.clientModel')"
          :invalid="Boolean(errors.client_model)"
          :described-by="errors.client_model ? 'logs-filter-error' : undefined"
          size="compact"
          data-1p-ignore="true"
          data-lpignore="true"
          @update:model-value="update('client_model', $event)"
        />
      </span>
      <AppSelect
        class="logs-filter__status"
        :model-value="draft.status"
        :label="t('monitor.logs.filters.status')"
        :options="statusOptions"
        size="compact"
        @update:model-value="update('status', $event)"
      />
      <AppButton variant="secondary" size="compact" @click="advancedOpen = true">
        <ListFilter :size="14" aria-hidden="true" />
        {{ t('monitor.logs.filters.more') }}
        <span v-if="advancedCount" class="logs-filter__count">{{ advancedCount }}</span>
      </AppButton>
      <AppButton type="submit" size="compact">{{ t('monitor.logs.filters.apply') }}</AppButton>
      <AppButton variant="ghost" size="compact" @click="reset">
        {{ t('monitor.logs.filters.reset') }}
      </AppButton>
    </div>

    <p v-if="firstError" id="logs-filter-error" class="logs-filter__error" role="alert">
      {{ firstError }}
    </p>
  </form>

  <LogsAdvancedFilterDrawer
    v-model:open="advancedOpen"
    :draft="draft"
    :errors="errors"
    :access-keys="accessKeys"
    :access-keys-failed="accessKeysFailed"
    @update-field="update"
    @apply="applyAdvanced"
    @reset="reset"
  />
</template>

<style scoped>
.logs-filter {
  display: grid;
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface-sunken);
}

.logs-filter__chips,
.logs-filter__row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}

.logs-filter__chips {
  flex-wrap: wrap;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 8px 10px;
}

.logs-filter__chips-label {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.logs-filter__chip {
  display: inline-flex;
  min-height: 24px;
  align-items: center;
  gap: 5px;
  border: 1px solid var(--color-border-control);
  border-radius: 999px;
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 2px 8px;
  font: inherit;
  font-size: var(--text-label-xs);
  cursor: pointer;
}

.logs-filter__chip:hover {
  border-color: var(--color-text-faint);
  color: var(--color-text);
}

.logs-filter__chip--fixed {
  cursor: default;
}

.logs-filter__chip--fixed:hover {
  border-color: var(--color-border-control);
  color: var(--color-text-muted);
}

.logs-filter__row {
  padding: 10px;
}

.logs-filter__group {
  width: 150px;
}

.logs-filter__model {
  display: block;
  width: auto;
  min-width: 130px;
  flex: 1 1 180px;
}

.logs-filter__model :deep(.app-text-input) {
  width: 100%;
}

.logs-filter__status {
  width: 108px;
}

.logs-filter__group :deep(.app-select__trigger),
.logs-filter__status :deep(.app-select__trigger) {
  width: 100%;
}

.logs-filter__count {
  display: inline-flex;
  min-width: 18px;
  height: 18px;
  align-items: center;
  justify-content: center;
  border-radius: 999px;
  background: var(--color-action-soft);
  color: var(--color-action);
  font-size: 11px;
}

.logs-filter__error {
  margin: -2px 10px 8px;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

@media (max-width: 1120px) {
  .logs-filter__row {
    flex-wrap: wrap;
  }
}

@media (max-width: 860px) {
  .logs-filter__row > :deep(.app-button),
  .logs-filter__row > :deep(.app-select__trigger) {
    min-height: var(--touch-target);
  }
}

@media (max-width: 560px) {
  .logs-filter__row {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .logs-filter__row > :deep(.app-popover),
  .logs-filter__group,
  .logs-filter__model,
  .logs-filter__status {
    width: 100%;
    grid-column: 1 / -1;
  }
}
</style>
