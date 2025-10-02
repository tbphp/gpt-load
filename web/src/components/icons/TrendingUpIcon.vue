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
    <polyline
      points="23 6 13.5 15.5 8.5 10.5 1 18"
      stroke="currentColor"
      stroke-width="2"
      fill="none"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
    <polyline
      points="17 6 23 6 23 12"
      stroke="currentColor"
      stroke-width="2"
      fill="none"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>
