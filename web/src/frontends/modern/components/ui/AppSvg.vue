<script setup lang="ts">
defineOptions({ inheritAttrs: false })
</script>

<template>
  <svg v-bind="$attrs"><slot /></svg>
</template>
