<script setup lang="ts">
import { Monitor, Moon, Sun } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { themes, usePreferences } from '@modern/app/preferences'
import PageHeader from '@modern/components/PageHeader.vue'
import { supportedLocales, type AppLocale } from '@shared/preferences/locale'
import FrontendPicker from './FrontendPicker.vue'
const { t } = useI18n()
const { theme, locale, setTheme, setLocale } = usePreferences()
const themeIcons = { system: Monitor, light: Sun, dark: Moon }
const languageNames = { 'zh-CN': '简体中文', 'en-US': 'English', 'ja-JP': '日本語' }
function changeLocale(event: Event): void {
  const value = (event.target as HTMLSelectElement).value as AppLocale
  if (supportedLocales.includes(value)) setLocale(value)
}
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t('pages.settings.title')" />
    <div class="modern-settings-stack">
      <section class="modern-panel">
        <header class="modern-panel-header">
          <h2>{{ t('appearance.title') }}</h2>
          <p>{{ t('appearance.description') }}</p>
        </header>
        <div class="modern-setting-row">
          <div class="modern-setting-copy">
            <strong>{{ t('appearance.theme') }}</strong>
          </div>
          <fieldset class="modern-theme-options">
            <legend class="modern-sr-only">{{ t('appearance.theme') }}</legend>
            <label v-for="option in themes" :key="option" class="modern-theme-option">
              <input
                type="radio"
                name="modern-theme"
                :value="option"
                :checked="theme === option"
                @change="setTheme(option)"
              />
              <component
                :is="themeIcons[option]"
                :size="17"
                :stroke-width="1.6"
                aria-hidden="true"
              />
              <span>{{ t(`appearance.themes.${option}`) }}</span>
            </label>
          </fieldset>
        </div>
        <div class="modern-setting-row">
          <div class="modern-setting-copy">
            <label for="modern-language"
              ><strong>{{ t('appearance.language') }}</strong></label
            >
          </div>
          <select id="modern-language" class="modern-select" :value="locale" @change="changeLocale">
            <option v-for="option in supportedLocales" :key="option" :value="option">
              {{ languageNames[option] }}
            </option>
          </select>
        </div>
      </section>
      <FrontendPicker />
    </div>
  </div>
</template>
