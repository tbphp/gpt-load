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

<style scoped>
.modern-page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-5);
}
.modern-page-header__copy {
  min-width: 0;
}
.modern-page-header h1 {
  color: var(--modern-text);
  font-size: var(--modern-font-size-title);
  font-weight: var(--modern-weight-semibold);
  letter-spacing: var(--modern-tracking-title);
  line-height: var(--modern-leading-title);
}
.modern-page-header p {
  margin-top: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-page-header__actions {
  display: flex;
  flex-wrap: wrap;
  flex-shrink: 0;
  align-items: center;
  gap: var(--modern-space-2);
}
@media (max-width: 760px) {
  .modern-page-header {
    align-items: flex-start;
  }
}
@media (max-width: 420px) {
  .modern-page-header {
    flex-direction: column;
    gap: var(--modern-space-4);
  }
  .modern-page-header__actions {
    width: 100%;
  }
}
</style>
