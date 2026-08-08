<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { GroupOptionDto } from '@/api/control/types'
import AppButton from '@/components/ui/AppButton.vue'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import FormField from '@/components/ui/FormField.vue'

import type { UsageFilterDraft, UsageFilterErrors } from './usage-filters'

const props = defineProps<{
  open: boolean
  draft: UsageFilterDraft
  errors: UsageFilterErrors
  groups: GroupOptionDto[]
  groupsFailed: boolean
  selfScoped?: boolean
}>()
const emit = defineEmits<{
  'update:open': [open: boolean]
  updateField: [field: keyof UsageFilterDraft, value: string]
  apply: []
  reset: []
}>()
const { t } = useI18n()

const option = (value: string, label: string) => ({ value, label })

function groupOptions() {
  const options = [option('', t('monitor.usage.filters.anyGroup'))]
  if (
    props.draft.group_id &&
    !props.groups.some((group) => String(group.id) === props.draft.group_id)
  ) {
    options.push(
      option(
        props.draft.group_id,
        t('monitor.usage.filters.deletedOrUnknown', { id: props.draft.group_id }),
      ),
    )
  }
  return [
    ...options,
    ...props.groups.map((group) => option(String(group.id), `${group.name} · #${group.id}`)),
  ]
}

function error(field: keyof UsageFilterErrors): string | undefined {
  const key = props.errors[field]
  return key ? t(key) : undefined
}
</script>

<template>
  <AppDrawer
    :open="open"
    appearance="ledger"
    :title="t('monitor.usage.filters.title')"
    :description="t('monitor.usage.filters.description')"
    :close-label="t('monitor.usage.filters.close')"
    @update:open="emit('update:open', $event)"
  >
    <form class="usage-filter-drawer" @submit.prevent="emit('apply')">
      <div class="usage-filter-drawer__grid">
        <FormField
          v-if="!selfScoped"
          id="usage-group"
          :label="t('monitor.usage.filters.group')"
          size="compact"
          :error="error('group_id')"
        >
          <template #default="{ describedBy, invalid }">
            <input
              v-if="groupsFailed"
              id="usage-group"
              :value="draft.group_id"
              inputmode="numeric"
              autocomplete="off"
              :aria-describedby="describedBy"
              :aria-invalid="invalid || undefined"
              @input="emit('updateField', 'group_id', ($event.target as HTMLInputElement).value)"
            />
            <AppSelect
              v-else
              id="usage-group"
              :model-value="draft.group_id"
              :label="t('monitor.usage.filters.group')"
              :options="groupOptions()"
              size="compact"
              :aria-describedby="describedBy"
              :aria-invalid="invalid || undefined"
              @update:model-value="emit('updateField', 'group_id', $event)"
            />
          </template>
        </FormField>
        <FormField
          id="usage-model"
          :label="t('monitor.usage.filters.model')"
          size="compact"
          :error="error('model')"
        >
          <template #default="{ describedBy, invalid }">
            <input
              id="usage-model"
              :value="draft.model"
              autocomplete="off"
              :placeholder="t('monitor.usage.filters.modelPlaceholder')"
              :aria-describedby="describedBy"
              :aria-invalid="invalid || undefined"
              @input="emit('updateField', 'model', ($event.target as HTMLInputElement).value)"
            />
          </template>
        </FormField>
      </div>
    </form>

    <template #footer>
      <AppButton variant="secondary" size="compact" @click="emit('reset')">
        {{ t('monitor.usage.filters.reset') }}
      </AppButton>
      <span class="usage-filter-drawer__actions">
        <AppButton variant="secondary" size="compact" @click="emit('update:open', false)">
          {{ t('common.cancel') }}
        </AppButton>
        <AppButton size="compact" @click="emit('apply')">{{
          t('monitor.usage.filters.apply')
        }}</AppButton>
      </span>
    </template>
  </AppDrawer>
</template>

<style scoped>
.usage-filter-drawer {
  min-width: 0;
  padding: 16px 0;
}

.usage-filter-drawer__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 10px;
}

.usage-filter-drawer__grid :deep(.app-select__trigger) {
  width: 100%;
}

.usage-filter-drawer__actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

@media (max-width: 520px) {
  .usage-filter-drawer__grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .usage-filter-drawer__actions {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
