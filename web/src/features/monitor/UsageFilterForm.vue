<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { GroupSummary } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import FormField from '@/components/ui/FormField.vue'

import type { UsageFilterDraft, UsageFilterErrors } from './usage-filters'

const props = defineProps<{
  draft: UsageFilterDraft
  errors: UsageFilterErrors
  groups: GroupSummary[]
  groupsFailed: boolean
  fetching: boolean
}>()
const emit = defineEmits<{
  updateField: [field: keyof UsageFilterDraft, value: string]
  apply: []
  reset: []
  refresh: []
}>()
const { t } = useI18n()

function error(field: keyof UsageFilterErrors): string | undefined {
  const key = props.errors[field]
  return key ? t(key) : undefined
}
</script>

<template>
  <form
    class="usage-filter-form"
    :aria-label="t('monitor.usage.filters.label')"
    @submit.prevent="emit('apply')"
  >
    <div class="usage-filter-grid">
      <FormField id="usage-range" :label="t('monitor.usage.filters.range')">
        <select
          id="usage-range"
          :value="draft.range"
          @change="emit('updateField', 'range', ($event.target as HTMLSelectElement).value)"
        >
          <option value="24h">{{ t('monitor.usage.filters.range24h') }}</option>
          <option value="30d">{{ t('monitor.usage.filters.range30d') }}</option>
        </select>
      </FormField>
      <FormField
        id="usage-group"
        :label="t('monitor.usage.filters.group')"
        :description="
          groupsFailed
            ? t('monitor.usage.filters.groupIdHelp')
            : t('monitor.usage.filters.groupHelp')
        "
        :error="error('group_id')"
      >
        <template #default="{ describedBy }">
          <input
            v-if="groupsFailed"
            id="usage-group"
            :value="draft.group_id"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            :aria-describedby="describedBy"
            :aria-invalid="error('group_id') ? 'true' : undefined"
            @input="emit('updateField', 'group_id', ($event.target as HTMLInputElement).value)"
          />
          <select
            v-else
            id="usage-group"
            :value="draft.group_id"
            :aria-describedby="describedBy"
            :aria-invalid="error('group_id') ? 'true' : undefined"
            @change="emit('updateField', 'group_id', ($event.target as HTMLSelectElement).value)"
          >
            <option value="">{{ t('monitor.usage.filters.anyGroup') }}</option>
            <option
              v-if="draft.group_id && !groups.some((group) => String(group.id) === draft.group_id)"
              :value="draft.group_id"
            >
              {{ t('monitor.usage.filters.deletedOrUnknown', { id: draft.group_id }) }}
            </option>
            <option v-for="group in groups" :key="group.id" :value="String(group.id)">
              {{ group.name }} · #{{ group.id }}
            </option>
          </select>
        </template>
      </FormField>
      <FormField
        id="usage-model"
        :label="t('monitor.usage.filters.model')"
        :description="t('monitor.usage.filters.modelHelp')"
        :error="error('model')"
      >
        <template #default="{ describedBy }">
          <input
            id="usage-model"
            :value="draft.model"
            type="text"
            autocomplete="off"
            :aria-describedby="describedBy"
            :aria-invalid="error('model') ? 'true' : undefined"
            @input="emit('updateField', 'model', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>
    </div>
    <div class="usage-filter-actions">
      <AppButton type="button" variant="secondary" :busy="fetching" @click="emit('refresh')">
        <RefreshCw :size="16" aria-hidden="true" />{{ t('monitor.usage.filters.refresh') }}
      </AppButton>
      <AppButton type="submit">{{ t('monitor.usage.filters.apply') }}</AppButton>
      <AppButton variant="ghost" @click="emit('reset')">
        {{ t('monitor.usage.filters.reset') }}
      </AppButton>
    </div>
  </form>
</template>

<style scoped>
.usage-filter-form {
  display: grid;
  gap: var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-4);
  box-shadow: var(--shadow-card);
}
.usage-filter-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(180px, 1fr));
  gap: var(--space-3);
}
.usage-filter-form input,
.usage-filter-form select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 8px 10px;
  font: inherit;
}
.usage-filter-actions {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-3);
}
@media (max-width: 720px) {
  .usage-filter-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
