<script setup lang="ts">
import { Monitor, Palette } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import SurfaceCard from '@/components/ui/SurfaceCard.vue'
import { useTheme, type AppTheme } from '@/features/preferences/theme'
import { supportedLocales, type AppLocale } from '@/i18n'
import { useAppI18n } from '@/i18n/context'

const appI18n = useAppI18n()
const theme = useTheme()
const { locale, t } = useI18n()
const themes: AppTheme[] = ['system', 'light', 'dark']

function setLocale(event: Event): void {
  const value = (event.target as HTMLSelectElement).value
  if (supportedLocales.includes(value as AppLocale)) {
    void appI18n.setLocale(value as AppLocale)
  }
}

function setTheme(event: Event): void {
  const value = (event.target as HTMLSelectElement).value
  if (themes.includes(value as AppTheme)) theme.setTheme(value as AppTheme)
}
</script>

<template>
  <SurfaceCard class="settings-card appearance-section">
    <header class="settings-card__heading">
      <span class="settings-card__icon"><Palette :size="18" aria-hidden="true" /></span>
      <div>
        <h2>{{ t('settings.appearance.title') }}</h2>
        <p>{{ t('settings.appearance.description') }}</p>
      </div>
    </header>
    <div class="appearance-section__grid">
      <label>
        <span>{{ t('settings.appearance.locale') }}</span>
        <select data-test="appearance-locale" :value="locale" @change="setLocale">
          <option value="zh-CN">{{ t('shell.localeZh') }}</option>
          <option value="en-US">{{ t('shell.localeEn') }}</option>
          <option value="ja-JP">{{ t('shell.localeJa') }}</option>
        </select>
      </label>
      <label>
        <span>{{ t('settings.appearance.theme') }}</span>
        <select data-test="appearance-theme" :value="theme.theme.value" @change="setTheme">
          <option value="system">{{ t('shell.themeSystem') }}</option>
          <option value="light">{{ t('shell.themeLight') }}</option>
          <option value="dark">{{ t('shell.themeDark') }}</option>
        </select>
      </label>
    </div>
    <p class="appearance-section__local-note">
      <Monitor :size="16" aria-hidden="true" />{{ t('settings.appearance.localOnly') }}
    </p>
  </SurfaceCard>
</template>

<style scoped>
.settings-card,
.settings-card__heading,
.appearance-section__grid,
.appearance-section label {
  display: grid;
}
.settings-card {
  gap: var(--space-4);
}
.settings-card__heading {
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
}
.settings-card__heading h2,
.settings-card__heading p,
.appearance-section__local-note {
  margin: 0;
}
.settings-card__heading h2 {
  font-size: 1rem;
}
.settings-card__heading p,
.appearance-section__local-note {
  color: var(--color-text-muted);
}
.settings-card__heading p {
  margin-top: var(--space-1);
}
.settings-card__icon {
  display: inline-flex;
  width: 36px;
  height: 36px;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-control);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.appearance-section__grid {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-4);
}
.appearance-section label {
  gap: var(--space-2);
  font-weight: 650;
}
.appearance-section select {
  min-height: 44px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface-secondary);
  color: var(--color-text);
  padding: var(--space-2) var(--space-3);
  font: inherit;
  cursor: pointer;
}
.appearance-section__local-note {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 0.8125rem;
}
@media (max-width: 640px) {
  .appearance-section__grid {
    grid-template-columns: 1fr;
  }
  .appearance-section select {
    font-size: 16px;
  }
}
</style>
