<script setup lang="ts">
import { Languages, Monitor, Moon, Sun } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { themes, usePreferences, type Theme } from '@modern/app/preferences'
import { AppSelectMenu } from '@modern/components/ui'
import { supportedLocales, type AppLocale } from '@shared/preferences/locale'

const { t } = useI18n()
const { theme, locale, setTheme, setLocale } = usePreferences()
const themeIcon = computed(() => ({ system: Monitor, light: Sun, dark: Moon })[theme.value])
const themeOptions = computed(() =>
  themes.map((value) => ({ value, label: t(`appearance.themes.${value}`) })),
)
const languageNames = { 'zh-CN': '简体中文', 'en-US': 'English', 'ja-JP': '日本語' }
const languageOptions = supportedLocales.map((value) => ({ value, label: languageNames[value] }))

function changeTheme(value: string): void {
  if (themes.includes(value as Theme)) setTheme(value as Theme)
}
function changeLocale(value: string): void {
  if (supportedLocales.includes(value as AppLocale)) setLocale(value as AppLocale)
}
</script>

<template>
  <AppSelectMenu
    :label="t('appearance.theme')"
    :icon="themeIcon"
    :model-value="theme"
    :options="themeOptions"
    @update:model-value="changeTheme"
  />
  <AppSelectMenu
    :label="t('appearance.language')"
    :icon="Languages"
    :model-value="locale"
    :options="languageOptions"
    @update:model-value="changeLocale"
  />
</template>
