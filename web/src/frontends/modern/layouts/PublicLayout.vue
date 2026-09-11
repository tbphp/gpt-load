<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import BrandLogo from '@modern/components/BrandLogo.vue'
import AppearanceMenu from './AppearanceMenu.vue'

const { t } = useI18n()
</script>

<template>
  <div class="modern-public-layout">
    <header class="modern-public-header">
      <RouterLink
        class="modern-public-brand"
        :to="{ name: 'modern-home' }"
        :aria-label="t('shell.goHome')"
      >
        <BrandLogo />
      </RouterLink>
      <div class="modern-public-actions"><AppearanceMenu /></div>
    </header>
    <main class="modern-public-content"><slot /></main>
  </div>
</template>

<style scoped>
.modern-public-layout {
  min-height: 100dvh;
  background: var(--modern-subtle);
}
.modern-public-header {
  display: flex;
  height: var(--modern-topbar-height);
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-4);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  padding: 0 var(--modern-content-inset);
}
.modern-public-brand {
  min-width: 0;
}
.modern-public-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-public-content {
  display: grid;
  min-height: calc(100dvh - var(--modern-topbar-height));
  align-items: center;
  justify-items: center;
  padding: var(--modern-space-8) var(--modern-content-inset);
}
</style>
