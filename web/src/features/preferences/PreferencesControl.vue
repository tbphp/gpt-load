<script setup lang="ts">
import { Languages, Monitor, Moon, Settings2, Sun } from '@lucide/vue'
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
  }>(),
  {
    compact: false,
  },
)

const emit = defineEmits<{
  'update:locale': [locale: AppLocale]
  'update:theme': [theme: AppTheme]
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
        <Settings2 :size="18" aria-hidden="true" />
      </IconButton>
    </template>
    <div class="preferences-panel">
      <header>
        <h2>{{ t('shell.preferences') }}</h2>
        <p>{{ t('shell.preferencesDescription') }}</p>
      </header>
      <fieldset>
        <legend><Languages :size="16" aria-hidden="true" />{{ t('shell.language') }}</legend>
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
        <legend><Monitor :size="16" aria-hidden="true" />{{ t('shell.theme') }}</legend>
        <label v-for="option in themeOptions" :key="option.value">
          <input
            type="radio"
            :name="`${identity}-theme`"
            :value="option.value"
            :checked="props.theme === option.value"
            @change="updateTheme"
          />
          <component :is="option.icon" :size="16" aria-hidden="true" />
          <span>{{ t(option.labelKey) }}</span>
        </label>
      </fieldset>
    </div>
  </AppPopover>

  <div v-else class="preferences-control preferences-panel">
    <fieldset>
      <legend><Languages :size="16" aria-hidden="true" />{{ t('shell.language') }}</legend>
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
      <legend><Monitor :size="16" aria-hidden="true" />{{ t('shell.theme') }}</legend>
      <label v-for="option in themeOptions" :key="option.value">
        <input
          type="radio"
          :name="`${identity}-theme`"
          :value="option.value"
          :checked="props.theme === option.value"
          @change="updateTheme"
        />
        <component :is="option.icon" :size="16" aria-hidden="true" />
        <span>{{ t(option.labelKey) }}</span>
      </label>
    </fieldset>
  </div>
</template>

<style>
.preferences-panel {
  display: grid;
  gap: var(--space-4);
}

.preferences-panel header h2,
.preferences-panel header p {
  margin: 0;
}

.preferences-panel header h2 {
  font-size: var(--text-lg);
}

.preferences-panel header p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.preferences-panel fieldset {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  border: 0;
  padding: 0;
}

.preferences-panel legend {
  display: flex;
  grid-column: 1 / -1;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 650;
}

.preferences-panel label {
  position: relative;
  display: flex;
  min-width: 0;
  min-height: var(--touch-target);
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: var(--space-2);
  cursor: pointer;
  text-align: center;
}

.preferences-panel label:has(input:checked) {
  border-color: var(--color-action);
  background: var(--color-action-soft);
  color: var(--color-action);
}

.preferences-panel input {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
}

.preferences-panel label:has(input:focus-visible) {
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
  box-shadow: var(--focus-ring);
}

.mobile-preferences .preferences-panel fieldset {
  grid-template-columns: 1fr;
}
</style>
