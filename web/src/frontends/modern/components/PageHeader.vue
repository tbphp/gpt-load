<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <header class="modern-page-header">
    <div class="modern-page-header__copy">
      <h1>{{ title }}</h1>
      <p v-if="description">{{ description }}</p>
    </div>
    <div v-if="$slots.default" class="modern-page-header__actions"><slot /></div>
  </header>
</template>
