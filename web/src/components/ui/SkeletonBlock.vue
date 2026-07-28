<script setup lang="ts">
withDefaults(
  defineProps<{
    height?: string
    rounded?: boolean
  }>(),
  {
    height: '1rem',
    rounded: true,
  },
)
</script>

<template>
  <span
    class="skeleton-block"
    :class="{ 'skeleton-block--rounded': rounded }"
    :style="{ '--skeleton-height': height }"
    aria-hidden="true"
  />
</template>

<style scoped>
.skeleton-block {
  display: block;
  width: 100%;
  height: var(--skeleton-height);
  background: linear-gradient(
    90deg,
    var(--color-surface-sunken),
    var(--color-border-subtle),
    var(--color-surface-sunken)
  );
  background-size: 200% 100%;
  animation: skeleton-shift 1.4s var(--easing-standard) infinite;
}

.skeleton-block--rounded {
  border-radius: var(--radius-control);
}

@keyframes skeleton-shift {
  to {
    background-position: -200% 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .skeleton-block {
    animation: none;
  }
}
</style>
