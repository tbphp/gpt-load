<script setup lang="ts">

const props = withDefaults(defineProps<{ size?: number }>(), { size: 24 });
</script>

<template>
  <svg
    :width="props.size"
    :height="props.size"
    viewBox="0 0 24 24"
    fill="none"
    aria-hidden="true"
    focusable="false"
  >
    <rect
      x="3"
      y="3"
      width="18"
      height="18"
      rx="2"
      ry="2"
      stroke="currentColor"
      stroke-width="2"
      fill="none"
    />
    <path d="M21 9H3" stroke="currentColor" stroke-width="2" />
    <path d="M9 21V9" stroke="currentColor" stroke-width="2" />
  </svg>
</template>
