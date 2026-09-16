<script setup lang="ts">
withDefaults(
  defineProps<{
    as?: 'div' | 'main' | 'section'
    centered?: boolean
  }>(),
  {
    as: 'div',
    centered: false,
  },
)
</script>

<template>
  <component :is="as" class="page-frame" :class="{ 'page-frame--centered': centered }">
    <div class="page-frame__inner">
      <slot />
    </div>
  </component>
</template>

<style scoped>
.page-frame {
  width: 100%;
  padding: var(--stage-padding-top) var(--stage-padding-inline) var(--stage-padding-bottom);
}

.page-frame--centered {
  display: grid;
  min-height: calc(100vh - var(--topbar-height));
  align-items: center;
}

.page-frame__inner {
  width: min(100%, var(--page-max));
  margin: 0 auto;
}

@media (max-width: 860px) {
  .page-frame {
    padding: var(--stage-padding-top-compact) var(--stage-padding-inline-compact)
      var(--stage-padding-bottom-compact);
  }

  .page-frame--centered {
    align-items: start;
  }
}
</style>
