<script setup lang="ts">
import { Check, Languages, Monitor, Moon, Sun } from '@lucide/vue'
import {
  DropdownMenuContent,
  DropdownMenuItemIndicator,
  DropdownMenuLabel,
  DropdownMenuPortal,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { themes, usePreferences, type Theme } from '@modern/app/preferences'
import { supportedLocales, type AppLocale } from '@shared/preferences/locale'

const { t } = useI18n()
const { theme, locale, setTheme, setLocale } = usePreferences()
const themeIcon = computed(() => ({ system: Monitor, light: Sun, dark: Moon })[theme.value])
const languageNames = { 'zh-CN': '简体中文', 'en-US': 'English', 'ja-JP': '日本語' }
function changeTheme(value: unknown): void {
  if (themes.includes(value as Theme)) setTheme(value as Theme)
}
function changeLocale(value: unknown): void {
  if (supportedLocales.includes(value as AppLocale)) setLocale(value as AppLocale)
}
</script>

<template>
  <DropdownMenuRoot>
    <DropdownMenuTrigger
      class="modern-icon-button"
      :aria-label="t('appearance.theme')"
      :title="t('appearance.theme')"
    >
      <component :is="themeIcon" :size="18" :stroke-width="1.8" aria-hidden="true" />
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent class="modern-menu" align="end" :side-offset="8">
        <DropdownMenuLabel class="modern-menu-label">{{ t('appearance.theme') }}</DropdownMenuLabel>
        <DropdownMenuRadioGroup :model-value="theme" @update:model-value="changeTheme">
          <DropdownMenuRadioItem
            v-for="option in themes"
            :key="option"
            class="modern-menu-item"
            :value="option"
          >
            {{ t(`appearance.themes.${option}`) }}
            <DropdownMenuItemIndicator
              ><Check :size="16" aria-hidden="true"
            /></DropdownMenuItemIndicator>
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
  <DropdownMenuRoot>
    <DropdownMenuTrigger
      class="modern-icon-button"
      :aria-label="t('appearance.language')"
      :title="t('appearance.language')"
      ><Languages :size="18" :stroke-width="1.8" aria-hidden="true"
    /></DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent class="modern-menu" align="end" :side-offset="8">
        <DropdownMenuLabel class="modern-menu-label">{{
          t('appearance.language')
        }}</DropdownMenuLabel>
        <DropdownMenuRadioGroup :model-value="locale" @update:model-value="changeLocale">
          <DropdownMenuRadioItem
            v-for="option in supportedLocales"
            :key="option"
            class="modern-menu-item"
            :value="option"
          >
            {{ languageNames[option] }}
            <DropdownMenuItemIndicator
              ><Check :size="16" aria-hidden="true"
            /></DropdownMenuItemIndicator>
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
