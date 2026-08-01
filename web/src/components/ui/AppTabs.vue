<script setup lang="ts">
import { TabsContent, TabsList, TabsRoot, TabsTrigger } from 'reka-ui'
import { ref, watch } from 'vue'

export interface AppTabItem {
  value: string
  label: string
  count?: string | number
}

const props = withDefaults(
  defineProps<{
    modelValue: string
    label: string
    items: AppTabItem[]
    appearance?: 'default' | 'detail'
  }>(),
  { appearance: 'default' },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const root = ref<HTMLElement>()

watch(
  () => props.modelValue,
  (value) => {
    const trigger = [...(root.value?.querySelectorAll<HTMLElement>('[data-tab-value]') ?? [])].find(
      (candidate) => candidate.dataset.tabValue === value,
    )
    trigger?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  },
  { flush: 'post' },
)
</script>

<template>
  <div ref="root" class="app-tabs" :class="`app-tabs--${appearance}`">
    <TabsRoot
      class="app-tabs__root"
      :model-value="modelValue"
      @update:model-value="(value) => typeof value === 'string' && emit('update:modelValue', value)"
    >
      <TabsList class="app-tabs__list" :aria-label="label">
        <TabsTrigger
          v-for="item in items"
          :key="item.value"
          class="app-tabs__trigger"
          :value="item.value"
          :data-tab-value="item.value"
        >
          <span>{{ item.label }}</span>
          <span v-if="item.count !== undefined" class="app-tabs__count">{{ item.count }}</span>
        </TabsTrigger>
      </TabsList>
      <TabsContent class="app-tabs__content" :value="modelValue">
        <slot />
      </TabsContent>
    </TabsRoot>
  </div>
</template>

<style scoped>
.app-tabs {
  display: block;
}
.app-tabs__root {
  display: grid;
  gap: var(--space-5);
}
.app-tabs__list {
  display: flex;
  max-width: 100%;
  gap: var(--space-1);
  overflow-x: auto;
  border-bottom: 1px solid var(--color-border-subtle);
}
.app-tabs__trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
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
  border-bottom-color: var(--color-action);
  color: var(--color-text);
}
.app-tabs__count {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-weight: 400;
}
.app-tabs--detail .app-tabs__root {
  gap: 0;
}
.app-tabs--detail .app-tabs__list {
  gap: var(--space-6);
  border-top: 1px solid var(--color-border-control);
}
.app-tabs--detail .app-tabs__trigger {
  min-width: var(--touch-target);
  min-height: var(--detail-tab-min-height);
  gap: 7px;
  padding: 0 1px;
  font-size: 13px;
  font-weight: 400;
}
.app-tabs--detail .app-tabs__trigger[data-state='active'] {
  font-weight: 620;
}
@media (max-width: 800px) {
  .app-tabs--detail .app-tabs__list {
    gap: 21px;
  }

  .app-tabs--detail .app-tabs__trigger {
    min-height: var(--detail-tab-min-height-compact);
  }
}
@media (prefers-reduced-motion: reduce) {
  .app-tabs__trigger {
    transition: none;
  }
}
</style>
