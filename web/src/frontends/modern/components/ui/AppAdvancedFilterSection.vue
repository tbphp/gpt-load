<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { ref, useId, watch } from 'vue'
import AppBadge from './AppBadge.vue'
import AppButton from './AppButton.vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{
  title: string
  compact?: boolean
  collapsible?: boolean
  activeCount?: number
  invalid?: boolean
}>()
const expanded = ref(false)
const contentId = useId()
watch(
  [() => props.activeCount, () => props.invalid],
  () => {
    if (props.activeCount || props.invalid) expanded.value = true
  },
  { immediate: true },
)
</script>

<template>
  <section
    class="modern-advanced-filter-section"
    :class="{ 'is-compact': compact, 'is-collapsible': collapsible }"
  >
    <AppButton
      v-if="collapsible"
      class="modern-advanced-filter-toggle"
      variant="ghost"
      :aria-expanded="expanded"
      :aria-controls="contentId"
      @click="expanded = !expanded"
    >
      <span>{{ title }}</span
      ><AppBadge v-if="activeCount" size="xs" tone="brand">{{ activeCount }}</AppBadge>
      <AppIcon :icon="ChevronDown" size="sm" :class="{ 'is-expanded': expanded }" />
    </AppButton>
    <h3 v-else>{{ title }}</h3>
    <div v-show="!collapsible || expanded" :id="contentId" class="modern-advanced-filter-fields">
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
.modern-advanced-filter-section.is-compact {
  gap: var(--modern-space-3);
  padding-block: var(--modern-space-2);
}
.modern-advanced-filter-section.is-compact .modern-advanced-filter-fields {
  gap: var(--modern-space-1-5) var(--modern-space-3);
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
  .modern-advanced-filter-fields > :deep(.modern-field) {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--modern-space-1-5);
  }
  .modern-advanced-filter-fields :deep(.modern-field label:not(.modern-sr-only)) {
    text-align: left;
    white-space: normal;
    color: var(--modern-text);
    font-size: var(--modern-font-size-secondary);
  }
}
.modern-advanced-filter-toggle {
  width: 100%;
  justify-content: flex-start;
  padding-inline: 0;
  color: var(--modern-text);
}
.modern-advanced-filter-toggle > span:first-child {
  flex: 1;
  text-align: left;
}
.modern-advanced-filter-toggle .is-expanded {
  transform: rotate(180deg);
}
.modern-advanced-filter-section.is-collapsible {
  gap: 0;
}
.is-collapsible .modern-advanced-filter-fields {
  padding-block: var(--modern-space-2) var(--modern-space-3);
}
</style>
