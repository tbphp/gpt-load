<script setup lang="ts">
import { X } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { enabledDataProtocols } from '@/api/control/protocols'
import type { AccessKeyOptionDto, GroupOptionDto } from '@/api/control/types'
import type { ChannelDto } from '@/app/resources/channels'
import AppButton from '@/components/ui/AppButton.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import AppTextInput from '@/components/ui/AppTextInput.vue'
import AccessKeySelect from '@/features/access-keys/AccessKeySelect.vue'

import { requestLogStatuses, type LogFilterDraft, type LogFilterErrors } from './log-filters'
import LogsAdvancedFilterDrawer from './LogsAdvancedFilterDrawer.vue'

interface AppliedChip {
  key: string
  label: string
}

const props = defineProps<{
  draft: LogFilterDraft
  errors: LogFilterErrors
  groups: GroupOptionDto[]
  channels: ChannelDto[]
  accessKeys: AccessKeyOptionDto[]
  groupsFailed: boolean
  channelsFailed: boolean
  accessKeysFailed: boolean
  appliedChips: AppliedChip[]
  advancedOpen: boolean
  selfScoped?: boolean
}>()
const emit = defineEmits<{
  'update:advancedOpen': [open: boolean]
  updateField: [field: keyof LogFilterDraft, value: string]
  removeFilter: [key: string]
  apply: []
  reset: []
}>()
const { t } = useI18n()

const groupOptions = computed(() => [
  { value: '', label: t('monitor.logs.filters.anyGroup') },
  ...props.groups.map((group) => ({
    value: String(group.id),
    label: `${group.name} · #${group.id}`,
  })),
])
const channelOptions = computed(() => {
  const options = [{ value: '', label: t('monitor.logs.filters.anyChannel') }]
  if (
    props.draft.channel_id &&
    !props.channels.some((channel) => channel.channel_id === props.draft.channel_id)
  ) {
    options.push({ value: props.draft.channel_id, label: props.draft.channel_id })
  }
  return [
    ...options,
    ...props.channels.map((channel) => ({ value: channel.channel_id, label: channel.name })),
  ]
})
const protocolOptions = computed(() => [
  { value: '', label: t('monitor.logs.filters.anyProtocol') },
  ...enabledDataProtocols.map((value) => ({ value, label: value })),
])
const statusOptions = computed(() => [
  { value: '', label: t('monitor.logs.filters.anyStatus') },
  ...requestLogStatuses.map((value) => ({
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
</script>

<template>
  <form
    class="logs-filter"
    :aria-label="t('monitor.logs.filters.label')"
    @submit.prevent="emit('apply')"
  >
    <div v-if="appliedChips.length" class="logs-filter__chips">
      <span class="logs-filter__chips-label">{{ t('monitor.logs.filters.applied') }}</span>
      <button
        v-for="chip in appliedChips"
        :key="chip.key"
        type="button"
        class="logs-filter__chip"
        :aria-label="t('monitor.logs.filters.remove', { value: chip.label })"
        @click="emit('removeFilter', chip.key)"
      >
        <span>{{ chip.label }}</span>
        <X :size="12" aria-hidden="true" />
      </button>
    </div>

    <div class="logs-filter__row">
      <AppSelect
        v-if="!selfScoped"
        class="logs-filter__group"
        :model-value="draft.group_id"
        :label="t('monitor.logs.filters.group')"
        :options="groupOptions"
        size="compact"
        :disabled="groupsFailed"
        @update:model-value="update('group_id', $event)"
      />
      <span v-if="!selfScoped" class="logs-filter__channel">
        <AppTextInput
          v-if="channelsFailed"
          :model-value="draft.channel_id"
          :label="t('monitor.logs.filters.channel')"
          :placeholder="t('monitor.logs.filters.channel')"
          :invalid="Boolean(errors.channel_id)"
          :described-by="errors.channel_id ? 'logs-filter-error' : undefined"
          size="compact"
          @update:model-value="update('channel_id', $event)"
        />
        <AppSelect
          v-else
          :model-value="draft.channel_id"
          :label="t('monitor.logs.filters.channel')"
          :options="channelOptions"
          size="compact"
          :aria-invalid="Boolean(errors.channel_id) || undefined"
          :aria-describedby="errors.channel_id ? 'logs-filter-error' : undefined"
          @update:model-value="update('channel_id', $event)"
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
      <span v-if="!selfScoped" class="logs-filter__access-key">
        <AppTextInput
          v-if="accessKeysFailed"
          :model-value="draft.access_key_id"
          :label="t('monitor.logs.filters.accessKey')"
          :placeholder="t('monitor.logs.filters.accessKey')"
          :invalid="Boolean(errors.access_key_id)"
          :described-by="errors.access_key_id ? 'logs-filter-error' : undefined"
          inputmode="numeric"
          size="compact"
          @update:model-value="update('access_key_id', $event)"
        />
        <AccessKeySelect
          v-else
          :model-value="draft.access_key_id ? Number(draft.access_key_id) : undefined"
          :options="accessKeys"
          :aria-invalid="Boolean(errors.access_key_id) || undefined"
          :aria-describedby="errors.access_key_id ? 'logs-filter-error' : undefined"
          @update:model-value="update('access_key_id', $event === undefined ? '' : String($event))"
        />
      </span>
      <AppSelect
        class="logs-filter__protocol"
        :model-value="draft.protocol"
        :label="t('monitor.logs.filters.protocol')"
        :options="protocolOptions"
        size="compact"
        @update:model-value="update('protocol', $event)"
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
      <AppButton type="submit" size="compact">{{ t('monitor.logs.filters.apply') }}</AppButton>
      <AppButton variant="secondary" size="compact" @click="emit('reset')">
        {{ t('monitor.logs.filters.reset') }}
      </AppButton>
    </div>

    <p v-if="firstError" id="logs-filter-error" class="logs-filter__error" role="alert">
      {{ firstError }}
    </p>
  </form>

  <LogsAdvancedFilterDrawer
    :open="advancedOpen"
    :draft="draft"
    :errors="errors"
    :self-scoped="selfScoped"
    @update-field="update"
    @update:open="emit('update:advancedOpen', $event)"
    @apply="emit('apply')"
    @reset="emit('reset')"
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

.logs-filter__row {
  --logs-filter-control-height: var(--control-compact);
  flex-wrap: wrap;
  padding: 10px;
}

.logs-filter__row :deep(.app-text-input) {
  min-height: var(--logs-filter-control-height);
  height: var(--logs-filter-control-height);
}

.logs-filter__row :deep(.app-text-input input) {
  min-height: 0;
  height: 100%;
}

.logs-filter__group {
  width: 150px;
}

.logs-filter__access-key {
  width: 170px;
}

.logs-filter__channel {
  width: 150px;
}

.logs-filter__protocol {
  width: 168px;
}

.logs-filter__model {
  display: block;
  width: auto;
  min-width: 130px;
  flex: 1 1 160px;
}

.logs-filter__channel :deep(.app-text-input),
.logs-filter__model :deep(.app-text-input),
.logs-filter__access-key :deep(.app-text-input),
.logs-filter__access-key :deep(.access-key-select),
.logs-filter__access-key :deep(.app-button) {
  width: 100%;
}

.logs-filter__access-key :deep(.access-key-select__label) {
  min-width: 0;
}

.logs-filter__status {
  width: 108px;
}

.logs-filter__group :deep(.app-select__trigger),
.logs-filter__status :deep(.app-select__trigger),
.logs-filter__channel :deep(.app-select__trigger),
.logs-filter__protocol :deep(.app-select__trigger) {
  width: 100%;
}

.logs-filter__error {
  margin: -2px 10px 8px;
  color: var(--color-danger);
  font-size: var(--text-label-xs);
}

@media (max-width: 860px) {
  .logs-filter__row {
    --logs-filter-control-height: var(--touch-target);
  }

  .logs-filter__row > :deep(.app-button),
  .logs-filter__row > :deep(.app-select__trigger),
  .logs-filter__channel :deep(.app-select__trigger),
  .logs-filter__access-key :deep(.app-button) {
    min-height: var(--touch-target);
  }
}

@media (max-width: 560px) {
  .logs-filter__row {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .logs-filter__access-key,
  .logs-filter__channel,
  .logs-filter__protocol,
  .logs-filter__group,
  .logs-filter__model,
  .logs-filter__status {
    width: 100%;
    grid-column: 1 / -1;
  }
}
</style>
