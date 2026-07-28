<script setup lang="ts">
import { ListFilter } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import type { AccessKeyOptionDto, GroupSummary } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import FormField from '@/components/ui/FormField.vue'

import type { LogFilterDraft, LogFilterErrors } from './log-filters'

const props = defineProps<{
  draft: LogFilterDraft
  errors: LogFilterErrors
  groups: GroupSummary[]
  accessKeys: AccessKeyOptionDto[]
  groupsFailed: boolean
  accessKeysFailed: boolean
  dirty: boolean
  refreshPending: boolean
}>()
const emit = defineEmits<{
  updateField: [field: keyof LogFilterDraft, value: string]
  apply: []
  reset: []
  refresh: []
}>()
const { t } = useI18n()

function error(field: keyof LogFilterDraft): string | undefined {
  const key = props.errors[field]
  return key ? t(key) : undefined
}
</script>

<template>
  <form
    class="logs-filter-form"
    data-test="logs-filter-form"
    :aria-label="t('monitor.logs.filters.label')"
    @submit.prevent="emit('apply')"
  >
    <div class="logs-filter-grid">
      <FormField id="logs-from" :label="t('monitor.logs.filters.from')" :error="error('from')">
        <template #default="{ describedBy }">
          <input
            id="logs-from"
            :value="draft.from"
            data-test="logs-from"
            type="datetime-local"
            :aria-describedby="describedBy"
            :aria-invalid="error('from') ? 'true' : undefined"
            @input="emit('updateField', 'from', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>
      <FormField id="logs-to" :label="t('monitor.logs.filters.to')" :error="error('to')">
        <template #default="{ describedBy }">
          <input
            id="logs-to"
            :value="draft.to"
            data-test="logs-to"
            type="datetime-local"
            :aria-describedby="describedBy"
            :aria-invalid="error('to') ? 'true' : undefined"
            @input="emit('updateField', 'to', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>
      <FormField
        id="logs-group"
        :label="t('monitor.logs.filters.group')"
        :description="t('monitor.logs.filters.groupHelp')"
        :error="error('group_id')"
      >
        <template #default="{ describedBy }">
          <select
            id="logs-group"
            :value="draft.group_id"
            data-test="logs-group"
            :aria-describedby="describedBy"
            :aria-invalid="error('group_id') ? 'true' : undefined"
            :disabled="groupsFailed"
            @change="emit('updateField', 'group_id', ($event.target as HTMLSelectElement).value)"
          >
            <option value="">{{ t('monitor.logs.filters.anyGroup') }}</option>
            <option
              v-if="draft.group_id && !groups.some((group) => String(group.id) === draft.group_id)"
              :value="draft.group_id"
            >
              #{{ draft.group_id }}
            </option>
            <option v-for="group in groups" :key="group.id" :value="String(group.id)">
              {{ group.name }} · #{{ group.id }}
            </option>
          </select>
        </template>
      </FormField>
      <FormField id="logs-model" :label="t('monitor.logs.filters.model')" :error="error('model')">
        <template #default="{ describedBy }">
          <input
            id="logs-model"
            :value="draft.model"
            data-test="logs-model"
            type="text"
            autocomplete="off"
            :aria-describedby="describedBy"
            :aria-invalid="error('model') ? 'true' : undefined"
            @input="emit('updateField', 'model', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>
      <FormField
        id="logs-access-key"
        :label="t('monitor.logs.filters.accessKey')"
        :error="error('access_key_id')"
      >
        <template #default="{ describedBy }">
          <select
            id="logs-access-key"
            :value="draft.access_key_id"
            data-test="logs-access-key"
            :aria-describedby="describedBy"
            :aria-invalid="error('access_key_id') ? 'true' : undefined"
            :disabled="accessKeysFailed"
            @change="
              emit('updateField', 'access_key_id', ($event.target as HTMLSelectElement).value)
            "
          >
            <option value="">{{ t('monitor.logs.filters.anyAccessKey') }}</option>
            <option
              v-if="
                draft.access_key_id &&
                !accessKeys.some((key) => String(key.id) === draft.access_key_id)
              "
              :value="draft.access_key_id"
            >
              #{{ draft.access_key_id }}
            </option>
            <option v-for="key in accessKeys" :key="key.id" :value="String(key.id)">
              {{ key.name }} · #{{ key.id }}
            </option>
          </select>
        </template>
      </FormField>
      <FormField
        id="logs-status"
        :label="t('monitor.logs.filters.status')"
        :error="error('status')"
      >
        <template #default="{ describedBy }">
          <select
            id="logs-status"
            :value="draft.status"
            data-test="logs-status"
            :aria-describedby="describedBy"
            :aria-invalid="error('status') ? 'true' : undefined"
            @change="emit('updateField', 'status', ($event.target as HTMLSelectElement).value)"
          >
            <option value="">{{ t('monitor.logs.filters.anyStatus') }}</option>
            <option value="success">{{ t('monitor.logs.status.success') }}</option>
            <option value="error">{{ t('monitor.logs.status.error') }}</option>
            <option value="incomplete">{{ t('monitor.logs.status.incomplete') }}</option>
            <option value="canceled">{{ t('monitor.logs.status.canceled') }}</option>
          </select>
        </template>
      </FormField>
      <FormField
        id="logs-request-id"
        :label="t('monitor.logs.filters.requestId')"
        :error="error('request_id')"
      >
        <template #default="{ describedBy }">
          <input
            id="logs-request-id"
            :value="draft.request_id"
            data-test="logs-request-id"
            type="text"
            autocomplete="off"
            :aria-describedby="describedBy"
            :aria-invalid="error('request_id') ? 'true' : undefined"
            @input="emit('updateField', 'request_id', ($event.target as HTMLInputElement).value)"
          />
        </template>
      </FormField>
    </div>
    <p v-if="dirty" class="logs-filter-dirty" data-test="logs-filter-dirty" role="status">
      <ListFilter :size="16" aria-hidden="true" />
      <span>{{ t('monitor.logs.filters.dirty') }}</span>
    </p>
    <div class="logs-filter-actions">
      <AppButton type="submit">{{ t('monitor.logs.filters.apply') }}</AppButton>
      <AppButton data-test="logs-reset" variant="ghost" @click="emit('reset')">
        {{ t('monitor.logs.filters.reset') }}
      </AppButton>
      <AppButton
        data-test="logs-refresh"
        variant="secondary"
        :busy="refreshPending"
        @click="emit('refresh')"
      >
        {{ t('monitor.logs.refresh') }}
      </AppButton>
    </div>
  </form>
</template>

<style scoped>
.logs-filter-form {
  display: grid;
  min-width: 0;
  gap: var(--space-4);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-4);
  box-shadow: var(--shadow-card);
}
.logs-filter-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(150px, 1fr));
  gap: var(--space-3);
}
.logs-filter-form input,
.logs-filter-form select {
  width: 100%;
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 8px 10px;
  font: inherit;
}
.logs-filter-actions,
.logs-filter-dirty {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.logs-filter-dirty {
  margin: 0;
  color: var(--color-text);
  font-weight: 650;
}
.logs-filter-dirty svg {
  color: var(--color-warning);
}
@media (max-width: 900px) {
  .logs-filter-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 560px) {
  .logs-filter-grid {
    grid-template-columns: 1fr;
  }
}
</style>
