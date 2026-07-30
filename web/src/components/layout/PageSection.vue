<script setup lang="ts">
withDefaults(
  defineProps<{
    as?: 'section' | 'div'
    title?: string
    titleId?: string
    divided?: boolean
  }>(),
  {
    as: 'section',
    title: undefined,
    titleId: undefined,
    divided: true,
  },
)
</script>

<template>
  <component :is="as" class="page-section" :class="{ 'page-section--divided': divided }">
    <header v-if="title || $slots.title || $slots.actions" class="page-section__header">
      <h2 v-if="title || $slots.title" :id="titleId" class="page-section__title">
        <slot name="title">{{ title }}</slot>
      </h2>
      <slot name="actions" />
    </header>
    <div class="page-section__content">
      <slot />
    </div>
  </component>
</template>

<style scoped>
.page-section {
  min-width: 0;
  padding: 22px 0 24px;
}

.page-section--divided {
  border-bottom: 1px solid var(--color-border-subtle);
}

.page-section__header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-3);
}

.page-section__title {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
  line-height: var(--line-compact);
}

.page-section__content {
  min-width: 0;
}
</style>
