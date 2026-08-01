<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppButton from '@/components/ui/AppButton.vue'

const props = defineProps<{
  page: number
  pageSize: number
  totalItems: number
  totalPages: number
}>()

defineEmits<{
  previous: []
  next: []
}>()

const { t } = useI18n()

const hasCurrentPage = computed(
  () => props.totalItems > 0 && props.page >= 1 && props.page <= props.totalPages,
)
const from = computed(() => (hasCurrentPage.value ? (props.page - 1) * props.pageSize + 1 : 0))
const to = computed(() =>
  hasCurrentPage.value ? Math.min(props.page * props.pageSize, props.totalItems) : 0,
)
</script>

<template>
  <nav class="pagination-bar" :aria-label="t('common.pagination.label')">
    <span class="pagination-bar__range" aria-live="polite">
      {{ t('common.pagination.range', { from, to, total: totalItems }) }}
    </span>
    <span class="pagination-bar__actions">
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
