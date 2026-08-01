<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'

const props = defineProps<{
  page: number
  pageSize: number
  totalItems: number
  totalPages: number
  pageSizes?: readonly (20 | 50 | 100)[]
  showPageSize?: boolean
}>()

const emit = defineEmits<{
  previous: []
  next: []
  'update:pageSize': [pageSize: 20 | 50 | 100]
}>()

const { t } = useI18n()

const hasCurrentPage = computed(
  () => props.totalItems > 0 && props.page >= 1 && props.page <= props.totalPages,
)
const from = computed(() => (hasCurrentPage.value ? (props.page - 1) * props.pageSize + 1 : 0))
const to = computed(() =>
  hasCurrentPage.value ? Math.min(props.page * props.pageSize, props.totalItems) : 0,
)
const pageSizes = computed(() => props.pageSizes ?? ([20, 50, 100] as const))

function updatePageSize(event: Event): void {
  const value = Number((event.target as HTMLSelectElement).value)
  if (value === 20 || value === 50 || value === 100) emit('update:pageSize', value)
}
</script>

<template>
  <nav class="pagination-bar" :aria-label="t('common.pagination.label')">
    <span class="pagination-bar__range" aria-live="polite">
      {{ t('common.pagination.range', { from, to, total: totalItems }) }}
    </span>
    <span class="pagination-bar__actions">
      <label v-if="showPageSize" class="pagination-bar__page-size">
        <span class="sr-only">{{ t('common.pagination.label') }}</span>
        <select :value="pageSize" @change="updatePageSize">
          <option v-for="size in pageSizes" :key="size" :value="size">{{ size }}</option>
        </select>
      </label>
      <AppButton
        variant="secondary"
        size="compact"
        :aria-label="t('common.pagination.previous')"
        :disabled="page <= 1"
        @click="$emit('previous')"
      >
        ←
      </AppButton>
      <span class="pagination-bar__page">{{ page }} / {{ totalPages }}</span>
      <AppButton
        variant="secondary"
        size="compact"
        :aria-label="t('common.pagination.next')"
        :disabled="totalPages === 0 || page >= totalPages"
        @click="$emit('next')"
      >
        →
      </AppButton>
    </span>
  </nav>
</template>

<style scoped>
.pagination-bar {
  display: flex;
  min-height: var(--pagination-min-height);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
  color: var(--color-text-faint);
  padding-top: var(--space-2);
  font-size: var(--text-label-xs);
}

.pagination-bar__actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

.pagination-bar__page {
  min-width: 44px;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  text-align: center;
}

.pagination-bar__page-size select {
  min-height: var(--touch-target);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding-inline: var(--space-2);
}

@media (max-width: 860px) {
  .pagination-bar :deep(.app-button) {
    min-height: var(--touch-target);
  }
}

@media (max-width: 480px) {
  .pagination-bar {
    align-items: stretch;
    flex-direction: column;
    padding-top: var(--space-3);
  }

  .pagination-bar__actions {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
  }
}
</style>
