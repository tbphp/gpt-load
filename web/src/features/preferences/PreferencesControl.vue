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
const localeOptions: Array<{ value: AppLocale; labelKey: string }> = [
  { value: 'zh-CN', labelKey: 'shell.localeZh' },
  { value: 'en-US', labelKey: 'shell.localeEn' },
  { value: 'ja-JP', labelKey: 'shell.localeJa' },
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
</script>

<template>
  <AppPopover
    v-if="compact"
    v-model:open="open"
    class="preferences-control preferences-control--compact"
  >
    <template #trigger>
      <IconButton :label="t('shell.preferences')" :pressed="open">
        <Menu :size="16" aria-hidden="true" />
      </IconButton>
    </template>
    <div class="preferences-panel">
      <fieldset>
        <legend>{{ t('shell.theme') }}</legend>
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
      </fieldset>
      <fieldset>
        <legend>{{ t('shell.language') }}</legend>
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
      </fieldset>
      <div v-if="showSignOut" class="preferences-panel__divider"></div>
      <button
        v-if="showSignOut"
        class="preferences-panel__action"
        type="button"
        @click="emit('sign-out')"
      >
        <LogOut :size="15" aria-hidden="true" />
        {{ t('shell.signOut') }}
      </button>
    </div>
  </AppPopover>

  <div v-else class="preferences-control preferences-panel">
    <fieldset>
      <legend><Languages :size="15" aria-hidden="true" />{{ t('shell.language') }}</legend>
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
    </fieldset>
    <fieldset>
      <legend><Monitor :size="15" aria-hidden="true" />{{ t('shell.theme') }}</legend>
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
    </fieldset>
    <div v-if="showSignOut" class="preferences-panel__divider"></div>
    <button
      v-if="showSignOut"
      class="preferences-panel__action"
      type="button"
      @click="emit('sign-out')"
    >
      <LogOut :size="15" aria-hidden="true" />
      {{ t('shell.signOut') }}
    </button>
  </div>
</template>

<style>
.preferences-panel {
  display: grid;
  min-width: 216px;
  gap: 10px;
}

.preferences-panel fieldset {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0;
  min-width: 0;
  margin: 0;
  overflow: hidden;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  padding: 0;
}

.preferences-panel legend {
  grid-column: 1 / -1;
  width: 100%;
  margin-bottom: 6px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  font-weight: 400;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.preferences-panel label {
  position: relative;
  display: flex;
  min-width: 0;
  min-height: var(--control-compact);
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
  background: var(--color-border-subtle);
}

.preferences-panel__action {
  display: flex;
  width: 100%;
  min-height: var(--control-sm);
  align-items: center;
  gap: var(--space-2);
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  padding: 7px 6px;
  font: inherit;
  font-size: var(--text-meta);
  cursor: pointer;
}

.preferences-panel__action:hover {
  background: var(--color-surface-sunken);
}

.mobile-preferences .preferences-panel fieldset {
  grid-template-columns: 1fr;
}

.mobile-preferences .preferences-panel label {
  min-height: var(--touch-target);
  border-top: 1px solid var(--color-border-control);
  border-left: 0;
}

.mobile-preferences .preferences-panel label:first-of-type {
  border-top: 0;
}
</style>
