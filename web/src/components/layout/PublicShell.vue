<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

import { homeLocation } from '@/app/route-locations'
import PreferencesControl from '@/features/preferences/PreferencesControl.vue'
import { useTheme } from '@/features/preferences/theme'
import { supportedLocales, type AppLocale } from '@/i18n'
import { useAppI18n } from '@/i18n/context'

const appI18n = useAppI18n()
const theme = useTheme()
const route = useRoute()
const { locale, t } = useI18n()
const currentLocale = computed(() => locale.value as AppLocale)

function setLocale(value: string): void {
  if (supportedLocales.includes(value as AppLocale)) {
    void appI18n.setLocale(value as AppLocale)
  }
}

watch(
  [() => route.meta.titleKey, locale],
  () => {
    const titleKey = route.meta.titleKey
    document.title = titleKey ? `${t(titleKey)} · ${t('common.appName')}` : t('common.appName')
  },
  { immediate: true },
)
</script>

<template>
  <div class="public-shell">
    <a class="skip-link" href="#main-content">{{ t('shell.skip') }}</a>
    <header class="public-topbar">
      <RouterLink
        class="public-brand"
        :to="homeLocation()"
        :aria-label="`${t('common.appName')} · ${t('shell.home')}`"
      >
        <span class="public-brand__mark" aria-hidden="true"></span>
        <span>{{ t('common.appName') }}</span>
      </RouterLink>
      <PreferencesControl
        compact
        :locale="currentLocale"
        :theme="theme.theme.value"
        @update:locale="setLocale"
        @update:theme="theme.setTheme"
      />
    </header>
    <div id="main-content" class="public-content" tabindex="-1">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.public-shell {
  min-height: 100vh;
}

.public-topbar {
  display: flex;
  height: var(--topbar-height);
  align-items: center;
  justify-content: space-between;
  gap: var(--space-5);
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface);
  padding: 0 var(--topbar-padding-inline);
}

.public-brand {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: 9px;
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 400;
  letter-spacing: -0.01em;
  white-space: nowrap;
}

.public-brand__mark {
  width: 7px;
  height: 18px;
  flex: none;
  background: var(--color-action);
}

.public-content {
  min-height: calc(100vh - var(--topbar-height));
}

@media (max-width: 860px) {
  .public-topbar {
    padding-inline: var(--space-4);
  }
}
</style>
