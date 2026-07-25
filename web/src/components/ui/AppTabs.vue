<script setup lang="ts">
import { TabsContent, TabsList, TabsRoot, TabsTrigger } from 'reka-ui'

export interface AppTabItem {
  value: string
  label: string
  testId?: string
}

defineProps<{
  modelValue: string
  label: string
  items: AppTabItem[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <TabsRoot
    class="app-tabs"
    :model-value="modelValue"
    @update:model-value="(value) => typeof value === 'string' && emit('update:modelValue', value)"
  >
    <TabsList class="app-tabs__list" :aria-label="label">
      <TabsTrigger
        v-for="item in items"
        :key="item.value"
        class="app-tabs__trigger"
        :value="item.value"
        :data-test="item.testId"
        @click="emit('update:modelValue', item.value)"
      >
        {{ item.label }}
      </TabsTrigger>
    </TabsList>
    <TabsContent class="app-tabs__content" :value="modelValue">
      <slot />
    </TabsContent>
  </TabsRoot>
</template>

<style scoped>
.app-tabs {
  display: grid;
  gap: var(--space-5);
}
.app-tabs__list {
  display: flex;
  max-width: 100%;
  gap: var(--space-1);
  overflow-x: auto;
  border-bottom: 1px solid var(--color-border);
}
.app-tabs__trigger {
  min-height: 44px;
  flex: 0 0 auto;
  border: 0;
  border-bottom: 2px solid transparent;
  background: transparent;
  color: var(--color-text-muted);
  padding: 9px 14px;
  font: inherit;
  font-weight: 650;
  cursor: pointer;
  transition:
    color var(--duration-fast) ease,
    border-color var(--duration-fast) ease;
}
.app-tabs__trigger[data-state='active'] {
  border-bottom-color: var(--color-primary);
  color: var(--color-text);
}
@media (prefers-reduced-motion: reduce) {
  .app-tabs__trigger {
    transition: none;
  }
}
</style>
