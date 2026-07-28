<script setup lang="ts">
defineOptions({ name: 'AppSurface' })

withDefaults(
  defineProps<{
    as?: 'article' | 'aside' | 'div' | 'section'
    variant?: 'default' | 'raised' | 'sunken'
    padded?: boolean
  }>(),
  {
    as: 'div',
    variant: 'default',
    padded: true,
  },
)
</script>

<template>
  <component
    :is="as"
    class="surface"
    :class="[`surface--${variant}`, { 'surface--flush': !padded }]"
  >
    <slot />
  </component>
</template>

<style scoped>
.surface {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
  padding: var(--space-6);
}

.surface--raised {
  background: var(--color-surface-raised);
  box-shadow: var(--shadow-card);
}

.surface--sunken {
  background: var(--color-surface-sunken);
}

.surface--flush {
  padding: 0;
}
</style>
