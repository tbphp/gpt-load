<script setup lang="ts">
withDefaults(
  defineProps<{
    height?: string
    width?: string
    rounded?: boolean
    concealed?: boolean
  }>(),
  {
    height: '1rem',
    width: '100%',
    rounded: true,
    concealed: false,
  },
)
</script>

<template>
  <span
    class="skeleton-block"
    :class="{
      'skeleton-block--rounded': rounded,
      'skeleton-block--concealed': concealed,
    }"
    :style="{ '--skeleton-height': height, '--skeleton-width': width }"
    aria-hidden="true"
  />
</template>

<style scoped>
.skeleton-block {
  position: relative;
  display: block;
  width: var(--skeleton-width);
  max-width: 100%;
  height: var(--skeleton-height);
  overflow: hidden;
  background: var(--color-skeleton-base);
}

.skeleton-block--rounded {
  border-radius: var(--radius-control);
}

.skeleton-block--concealed {
  visibility: hidden;
}

.skeleton-block::after {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    90deg,
    transparent 0%,
    var(--color-skeleton-highlight) 50%,
    transparent 100%
  );
  content: '';
  transform: translateX(-100%);
  animation: skeleton-shift var(--duration-skeleton) linear infinite;
}

@keyframes skeleton-shift {
  to {
    transform: translateX(100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .skeleton-block::after {
    animation: none;
  }
}
</style>
