<script setup lang="ts">
import { Languages, Monitor, Moon, Sun } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { useMessages } from '@modern/app/messages'
import { themes, usePreferences, type Theme } from '@modern/app/preferences'
import { useDraftGuards } from '@modern/components/draft-guards'
import { AppSelectMenu } from '@modern/components/ui'
import { switchFrontend } from '@shared/frontend/preference'
import { supportedLocales, type AppLocale } from '@shared/preferences/locale'

const { t } = useI18n()
const route = useRoute()
const messages = useMessages()
const draftGuards = useDraftGuards()
const switchingFrontend = ref(false)
const { theme, locale, setTheme, setLocale } = usePreferences()
const themeIcon = computed(() => ({ system: Monitor, light: Sun, dark: Moon })[theme.value])
const themeOptions = computed(() =>
  themes.map((value) => ({ value, label: t(`appearance.themes.${value}`) })),
)
const languageNames = { 'zh-CN': '简体中文', 'en-US': 'English', 'ja-JP': '日本語' }
const languageOptions = supportedLocales.map((value) => ({ value, label: languageNames[value] }))
const frontendGroup = computed(() => ({
  label: t('interfaceSettings'),
  modelValue: 'modern',
  options: ['modern', 'classic'].map((value) => ({
    value,
    label: t(`frontend.${value}.title`),
  })),
  disabled: switchingFrontend.value,
}))

function changeTheme(value: string): void {
  if (themes.includes(value as Theme)) setTheme(value as Theme)
}
function changeLocale(value: string): void {
  if (supportedLocales.includes(value as AppLocale)) setLocale(value as AppLocale)
}
async function changeFrontend(value: string): Promise<void> {
  if (value !== 'classic' || switchingFrontend.value) return
  switchingFrontend.value = true
  try {
    const path = route.meta.requiresAuth ? '/' : '/login'
    if (!(await draftGuards.run(() => switchFrontend(value, path)))) switchingFrontend.value = false
  } catch {
    switchingFrontend.value = false
    messages.show({ text: t('frontend.saveFailed'), tone: 'danger' })
  }
}
</script>

<template>
  <AppSelectMenu
    :label="t('appearance.theme')"
    :icon="themeIcon"
    :model-value="theme"
    :options="themeOptions"
    :secondary-group="frontendGroup"
    @update:model-value="changeTheme"
    @update:secondary-value="changeFrontend"
  />
  <AppSelectMenu
    :label="t('appearance.language')"
    :icon="Languages"
    :model-value="locale"
    :options="languageOptions"
    @update:model-value="changeLocale"
  />
</template>
