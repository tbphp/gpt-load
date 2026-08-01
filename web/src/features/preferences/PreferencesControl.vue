<script setup lang="ts">
import { Languages, LogOut, Menu, Monitor, Moon, Sun } from '@lucide/vue'
import { useId, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import AppPopover from '@/components/ui/AppPopover.vue'
import IconButton from '@/components/ui/IconButton.vue'
import type { AppLocale } from '@/i18n'

import type { AppTheme } from './theme'

const props = withDefaults(
  defineProps<{
    locale: AppLocale
    theme: AppTheme
    compact?: boolean
    showSignOut?: boolean
  }>(),
  {
    compact: false,
    showSignOut: false,
  },
)

const emit = defineEmits<{
  'update:locale': [locale: AppLocale]
  'update:theme': [theme: AppTheme]
  'sign-out': []
}>()

const { t } = useI18n()
const open = ref(false)
const identity = useId()
const localeOptions: Array<{ value: AppLocale; labelKey: string; compactLabelKey: string }> = [
  { value: 'zh-CN', labelKey: 'shell.localeZh', compactLabelKey: 'shell.localeZhShort' },
  { value: 'en-US', labelKey: 'shell.localeEn', compactLabelKey: 'shell.localeEnShort' },
  { value: 'ja-JP', labelKey: 'shell.localeJa', compactLabelKey: 'shell.localeJaShort' },
]
const themeOptions: Array<{
  value: AppTheme
  labelKey: string
  icon: typeof Monitor
}> = [
  { value: 'system', labelKey: 'shell.themeSystem', icon: Monitor },
  { value: 'light', labelKey: 'shell.themeLight', icon: Sun },
  { value: 'dark', labelKey: 'shell.themeDark', icon: Moon },
]

function updateLocale(event: Event): void {
  const input = event.target as HTMLInputElement
  if (input.checked) emit('update:locale', input.value as AppLocale)
}

function updateTheme(event: Event): void {
  const input = event.target as HTMLInputElement
  if (input.checked) emit('update:theme', input.value as AppTheme)
}

function signOut(): void {
  open.value = false
  emit('sign-out')
}
</script>

<template>
  <AppPopover
    v-if="compact"
    v-model:open="open"
    class="preferences-control preferences-control--compact"
    content-class="app-popover__content--preferences"
  >
    <template #trigger>
      <IconButton
        class="preferences-trigger"
        :label="t('shell.preferences')"
        :pressed="open"
        variant="surface"
        size="compact"
      >
        <Menu :size="15" aria-hidden="true" />
      </IconButton>
    </template>
    <div class="preferences-panel">
      <div class="preferences-panel__group">
        <span class="preferences-panel__label">{{ t('shell.theme') }}</span>
        <div class="preferences-panel__segments" role="group" :aria-label="t('shell.theme')">
          <label v-for="option in themeOptions" :key="option.value">
            <input
              type="radio"
              :name="`${identity}-theme`"
              :value="option.value"
              :checked="props.theme === option.value"
              @change="updateTheme"
            />
            <component :is="option.icon" :size="14" aria-hidden="true" />
            <span class="sr-only">{{ t(option.labelKey) }}</span>
          </label>
        </div>
      </div>
      <div class="preferences-panel__group">
        <span class="preferences-panel__label">{{ t('shell.language') }}</span>
        <div class="preferences-panel__segments" role="group" :aria-label="t('shell.language')">
          <label v-for="option in localeOptions" :key="option.value">
            <input
              type="radio"
              :name="`${identity}-locale`"
              :value="option.value"
              :checked="props.locale === option.value"
              @change="updateLocale"
            />
            <span>{{ t(option.compactLabelKey) }}</span>
          </label>
        </div>
      </div>
      <div v-if="showSignOut" class="preferences-panel__divider"></div>
      <button v-if="showSignOut" class="preferences-panel__action" type="button" @click="signOut">
        <LogOut :size="15" aria-hidden="true" />
        {{ t('shell.signOut') }}
      </button>
    </div>
  </AppPopover>

  <div v-else class="preferences-control preferences-panel">
    <div class="preferences-panel__group">
      <span class="preferences-panel__label">
        <Languages :size="15" aria-hidden="true" />{{ t('shell.language') }}
      </span>
      <div class="preferences-panel__segments" role="group" :aria-label="t('shell.language')">
        <label v-for="option in localeOptions" :key="option.value">
          <input
            type="radio"
            :name="`${identity}-locale`"
            :value="option.value"
            :checked="props.locale === option.value"
            @change="updateLocale"
          />
          <span>{{ t(option.labelKey) }}</span>
        </label>
      </div>
    </div>
    <div class="preferences-panel__group">
      <span class="preferences-panel__label">
        <Monitor :size="15" aria-hidden="true" />{{ t('shell.theme') }}
      </span>
      <div class="preferences-panel__segments" role="group" :aria-label="t('shell.theme')">
        <label v-for="option in themeOptions" :key="option.value">
          <input
            type="radio"
            :name="`${identity}-theme`"
            :value="option.value"
            :checked="props.theme === option.value"
            @change="updateTheme"
          />
          <component :is="option.icon" :size="15" aria-hidden="true" />
          <span>{{ t(option.labelKey) }}</span>
        </label>
      </div>
    </div>
    <div v-if="showSignOut" class="preferences-panel__divider"></div>
    <button v-if="showSignOut" class="preferences-panel__action" type="button" @click="signOut">
      <LogOut :size="15" aria-hidden="true" />
      {{ t('shell.signOut') }}
    </button>
  </div>
</template>

<style>
.preferences-panel {
  display: grid;
  width: 100%;
  gap: 10px;
}

.preferences-panel__group {
  display: grid;
  gap: 6px;
  min-width: 0;
}

.preferences-panel__label {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--color-text-faint);
  padding: 0 2px;
  font-size: var(--text-label-xs);
  font-weight: 400;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.preferences-panel__segments {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  overflow: hidden;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
}

.preferences-panel label {
  position: relative;
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: center;
  gap: 4px;
  border-left: 1px solid var(--color-border-control);
  background: var(--color-surface);
  color: var(--color-text-muted);
  padding: 6px 4px;
  font-size: var(--text-sm);
  cursor: pointer;
  text-align: center;
  transition:
    color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.preferences-panel label:first-of-type {
  border-left: 0;
}

.preferences-panel label:has(input:checked) {
  background: var(--color-text);
  color: var(--color-surface);
  font-weight: 560;
}

.preferences-panel input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
}

.preferences-panel label:has(input:focus-visible) {
  z-index: 1;
  outline: 2px solid var(--color-focus);
  outline-offset: -2px;
  box-shadow: var(--focus-ring);
}

.preferences-panel__divider {
  height: 1px;
  margin: 0 -10px;
  background: var(--color-border-subtle);
}

.preferences-panel__action {
  display: flex;
  width: 100%;
  align-items: center;
  gap: var(--space-2);
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  padding: 7px 6px;
  font: inherit;
  font-size: 12.5px;
  cursor: pointer;
}

.preferences-panel__action:hover {
  background: var(--color-surface-sunken);
}

.app-popover__content.app-popover__content--preferences {
  width: auto;
  min-width: 216px;
  border-color: var(--color-border-control);
  border-radius: 10px;
  padding: 10px;
}

@media (max-width: 860px) {
  .preferences-trigger {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .app-popover__content.app-popover__content--preferences {
    min-width: 244px;
  }

  .preferences-panel label,
  .preferences-panel__action {
    min-height: var(--touch-target);
  }
}
</style>
