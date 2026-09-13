<script setup lang="ts">
import { useI18n } from 'vue-i18n'

defineProps<{ loading: boolean }>()
const { t } = useI18n()
</script>

<template>
  <div v-if="loading" class="modern-loading-indicator" role="status">
    <span class="modern-loading-indicator-track" aria-hidden="true" />
    <span class="modern-sr-only">{{ t('ui.loading') }}</span>
  </div>
</template>

<style scoped>
.modern-loading-indicator {
  position: absolute;
  inset: auto 0 0;
  height: var(--modern-loading-line);
  overflow: hidden;
  background: var(--modern-accent-soft);
  pointer-events: none;
}
.modern-loading-indicator-track {
  display: block;
  width: 35%;
  height: 100%;
  background: var(--modern-action);
  animation: modern-loading-travel var(--modern-motion-loading) linear infinite;
}
@keyframes modern-loading-travel {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(390%);
  }
}
@media (prefers-reduced-motion: reduce) {
  .modern-loading-indicator-track {
    width: 100%;
  }
}
</style>
