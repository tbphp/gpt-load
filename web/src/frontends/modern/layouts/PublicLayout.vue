<script setup lang="ts">
import { ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import BrandLogo from '@modern/components/BrandLogo.vue'
import { usePreferences } from '@modern/app/preferences'
import AppearanceMenu from './AppearanceMenu.vue'

defineProps<{ restoringSession?: boolean }>()
const { t } = useI18n()
const { sidebarCollapsed, resolvedTheme } = usePreferences()
const mascot = ref<InstanceType<typeof BrandLogo>>()
const mascotHint = useId()
</script>

<template>
  <div class="modern-public-layout">
    <header class="modern-public-header">
      <RouterLink
        class="modern-public-brand"
        :class="{
          'is-restoring-session': restoringSession,
          'is-compact': restoringSession && sidebarCollapsed,
        }"
        :to="{ name: 'modern-home' }"
        :aria-label="t('shell.goHome')"
        :aria-describedby="mascotHint"
        @keydown.space.prevent="!$event.repeat && mascot?.nudge()"
      >
        <BrandLogo
          ref="mascot"
          :compact="restoringSession && sidebarCollapsed"
          :resolved-theme="resolvedTheme"
        />
      </RouterLink>
      <span :id="mascotHint" class="modern-sr-only">{{ t('shell.mascotHint') }}</span>
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
  --modern-mascot-backdrop: var(--modern-surface);
  min-width: 0;
}
/* 恢复会话后会切到侧栏，品牌预先使用相同的位置和尺寸，避免刷新时横向跳动。 */
.modern-public-brand.is-restoring-session {
  position: absolute;
  top: 0;
  left: 0;
  display: flex;
  align-items: center;
  width: calc(var(--modern-sidebar-expanded) - var(--modern-line-width));
  height: var(--modern-topbar-height);
  padding-inline: calc(2 * var(--modern-space-3)) var(--modern-space-3);
}
.modern-public-brand.is-restoring-session.is-compact {
  width: calc(var(--modern-sidebar-collapsed) - var(--modern-line-width));
  justify-content: center;
  padding-inline: var(--modern-space-3);
}
.modern-public-actions {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--modern-space-1);
  margin-left: auto;
}
.modern-public-content {
  display: grid;
  min-height: calc(100dvh - var(--modern-topbar-height));
  align-items: center;
  justify-items: center;
  padding: var(--modern-space-8) var(--modern-content-inset);
}
@media (max-width: 760px) {
  .modern-public-brand.is-restoring-session {
    display: none;
  }
}
</style>
