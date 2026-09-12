<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <section class="modern-form-section">
    <header class="modern-form-section-heading">
      <div>
        <h3>{{ title }}</h3>
        <p v-if="description">{{ description }}</p>
      </div>
      <div v-if="$slots.actions" class="modern-form-section-actions"><slot name="actions" /></div>
    </header>
    <div class="modern-form-section-body"><slot /></div>
  </section>
</template>

<style scoped>
.modern-form-section {
  min-width: 0;
}
.modern-form-section + .modern-form-section {
  padding-top: var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-form-section-heading {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
  margin-bottom: var(--modern-space-3);
}
.modern-form-section-heading > div:first-child {
  flex: 1;
  min-width: 0;
}
.modern-form-section-heading h3 {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-form-section-heading p {
  margin-top: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-form-section-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-form-section-body {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
}
</style>
