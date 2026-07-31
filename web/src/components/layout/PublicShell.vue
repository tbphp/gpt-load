<script setup lang="ts">
import { Moon, Sun } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

import { homeLocation } from '@/app/route-locations'
import AppSelect from '@/components/ui/AppSelect.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { useTheme } from '@/features/preferences/theme'
import { supportedLocales, type AppLocale } from '@/i18n'
import { useAppI18n } from '@/i18n/context'

const appI18n = useAppI18n()
const theme = useTheme()
const route = useRoute()
const { locale, t } = useI18n()
const currentLocale = computed(() => locale.value as AppLocale)
const systemDark = ref(false)
let colorScheme: MediaQueryList | undefined
const languageOptions = computed(() => [
  { value: 'zh-CN', label: t('shell.localeZh') },
  { value: 'en-US', label: t('shell.localeEn') },
  { value: 'ja-JP', label: t('shell.localeJa') },
])
const darkTheme = computed(
  () => theme.theme.value === 'dark' || (theme.theme.value === 'system' && systemDark.value),
)
const themeActionLabel = computed(() =>
  darkTheme.value ? t('shell.useLightTheme') : t('shell.useDarkTheme'),
)

function setLocale(value: string): void {
  if (supportedLocales.includes(value as AppLocale)) {
    void appI18n.setLocale(value as AppLocale)
  }
}

function toggleTheme(): void {
  theme.setTheme(darkTheme.value ? 'light' : 'dark')
}

function syncSystemTheme(event: MediaQueryListEvent | MediaQueryList): void {
  systemDark.value = event.matches
}

onMounted(() => {
  colorScheme = window.matchMedia('(prefers-color-scheme: dark)')
  syncSystemTheme(colorScheme)
  colorScheme.addEventListener('change', syncSystemTheme)
})

onBeforeUnmount(() => {
  colorScheme?.removeEventListener('change', syncSystemTheme)
})

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

      <div class="public-tools">
        <AppSelect
          class="public-language"
          :model-value="currentLocale"
          :label="t('shell.language')"
          :options="languageOptions"
          @update:model-value="setLocale"
        />
        <IconButton
          class="public-theme-action"
          :label="themeActionLabel"
          size="compact"
          @click="toggleTheme"
        >
          <Sun v-if="darkTheme" :size="15" aria-hidden="true" />
          <Moon v-else :size="15" aria-hidden="true" />
        </IconButton>
      </div>
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

.public-tools {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.public-tools :deep(.public-language.app-select__trigger) {
  width: auto;
  min-width: 126px;
  min-height: var(--control-compact);
  height: var(--control-compact);
  padding: 0 var(--space-2);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.public-content {
  min-height: calc(100vh - var(--topbar-height));
}

@media (max-width: 860px) {
  .public-topbar {
    padding-inline: var(--space-4);
  }
}

@media (pointer: coarse) {
  .public-tools :deep(.public-language.app-select__trigger) {
    min-height: var(--touch-target);
    height: var(--touch-target);
  }
}
</style>
