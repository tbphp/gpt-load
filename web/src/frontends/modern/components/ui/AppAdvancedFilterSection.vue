<script setup lang="ts">
defineProps<{ title: string }>()
</script>

<template>
  <section class="modern-advanced-filter-section">
    <h3>{{ title }}</h3>
    <div class="modern-advanced-filter-fields">
      <slot />
    </div>
  </section>
</template>

<style scoped>
.modern-advanced-filter-section {
  display: grid;
  grid-template-columns: 52px minmax(0, 1fr);
  align-items: start;
  gap: var(--modern-space-4);
  padding-block: var(--modern-space-3);
}
.modern-advanced-filter-section + .modern-advanced-filter-section {
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-advanced-filter-section h3 {
  padding-top: var(--modern-space-1-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-medium);
  letter-spacing: var(--modern-tracking-label);
}
.modern-advanced-filter-fields {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(204px, 100%), 1fr));
  gap: var(--modern-space-2) var(--modern-space-4);
  min-width: 0;
}
/* 仅直接字段使用横排标签；范围输入内部的字段保持自身布局。 */
.modern-advanced-filter-fields > :deep(.modern-field) {
  grid-template-columns: 58px minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-advanced-filter-fields :deep(.modern-field label:not(.modern-sr-only)) {
  overflow: hidden;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-regular);
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-advanced-filter-fields :deep(.modern-field-error) {
  grid-column: 1 / -1;
}
@media (max-width: 760px) {
  .modern-advanced-filter-section {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--modern-space-2);
  }
}
</style>
