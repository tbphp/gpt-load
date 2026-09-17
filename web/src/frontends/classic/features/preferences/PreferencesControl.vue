<script setup lang="ts">
import { Languages, LogOut, Menu, Monitor, Moon, Sun } from '@lucide/vue'
import { useId, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import AppPopover from '@/components/ui/AppPopover.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import IconButton from '@/components/ui/IconButton.vue'
import type { AppLocale } from '@/i18n'

import type { AppTheme } from './theme'

const props = withDefaults(
  defineProps<{
    locale: AppLocale
    theme: AppTheme
    compact?: boolean
    showSignOut?: boolean
    triggerLabel?: string
  }>(),
  {
    compact: false,
    showSignOut: false,
    triggerLabel: '',
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
const githubURL = 'https://github.com/tbphp/gpt-load'
const telegramURL = 'https://t.me/+GHpy5SwEllg3MTUx'
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
  close()
  emit('sign-out')
}

function close(): void {
  open.value = false
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
        :label="triggerLabel || t('shell.preferences')"
        :pressed="open"
        variant="surface"
        size="compact"
      >
        <Menu :size="15" aria-hidden="true" />
      </IconButton>
    </template>
    <div class="preferences-panel">
      <div v-if="$slots['mobile-navigation']" class="preferences-panel__mobile-navigation">
        <slot name="mobile-navigation" :close="close" />
      </div>
      <div
        v-if="$slots['mobile-navigation']"
        class="preferences-panel__mobile-navigation-divider"
      ></div>
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
      <div class="preferences-panel__divider"></div>
      <div class="preferences-panel__group">
        <div class="preferences-panel__project-meta">
          <AppTooltip content="GitHub">
            <a
              class="preferences-panel__brand-link preferences-panel__brand-link--github"
              :href="githubURL"
              target="_blank"
              rel="noopener noreferrer"
              aria-label="GitHub"
              @click="close"
            >
              <!-- Source: https://github.com/lobehub/lobe-icons/blob/master/packages/static-svg/icons/github.svg -->
              <svg
                class="preferences-panel__brand-icon"
                viewBox="0 0 24 24"
                fill="currentColor"
                fill-rule="evenodd"
                aria-hidden="true"
                focusable="false"
              >
                <path
                  d="M12 0c6.63 0 12 5.276 12 11.79-.001 5.067-3.29 9.567-8.175 11.187-.6.118-.825-.25-.825-.56 0-.398.015-1.665.015-3.242 0-1.105-.375-1.813-.81-2.181 2.67-.295 5.475-1.297 5.475-5.822 0-1.297-.465-2.344-1.23-3.169.12-.295.54-1.503-.12-3.125 0 0-1.005-.324-3.3 1.209a11.32 11.32 0 00-3-.398c-1.02 0-2.04.133-3 .398-2.295-1.518-3.3-1.209-3.3-1.209-.66 1.622-.24 2.83-.12 3.125-.765.825-1.23 1.887-1.23 3.169 0 4.51 2.79 5.527 5.46 5.822-.345.294-.66.81-.765 1.577-.69.31-2.415.81-3.495-.973-.225-.354-.9-1.223-1.845-1.209-1.005.015-.405.56.015.781.51.28 1.095 1.327 1.23 1.666.24.663 1.02 1.93 4.035 1.385 0 .988.015 1.916.015 2.196 0 .31-.225.664-.825.56C3.303 21.374-.003 16.867 0 11.791 0 5.276 5.37 0 12 0z"
                />
              </svg>
            </a>
          </AppTooltip>
          <span class="preferences-panel__star">{{ t('shell.starInvitation') }}</span>
          <AppTooltip content="Telegram">
            <a
              class="preferences-panel__brand-link preferences-panel__brand-link--telegram"
              :href="telegramURL"
              target="_blank"
              rel="noopener noreferrer"
              aria-label="Telegram"
              @click="close"
            >
              <!-- Source: https://telegram.org/img/t_logo.svg -->
              <svg
                class="preferences-panel__brand-icon"
                viewBox="0 0 128 128"
                fill="none"
                fill-rule="evenodd"
                aria-hidden="true"
                focusable="false"
              >
                <defs>
                  <linearGradient
                    :id="`${identity}-telegram-logo`"
                    x1="50%"
                    x2="50%"
                    y1="0%"
                    y2="99.258%"
                  >
                    <stop offset="0%" stop-color="#2aabee" />
                    <stop offset="100%" stop-color="#229ed9" />
                  </linearGradient>
                </defs>
                <circle
                  cx="64"
                  cy="64"
                  r="64"
                  :fill="`url(#${identity}-telegram-logo)`"
                  fill-rule="nonzero"
                />
                <path
                  fill="#fff"
                  fill-rule="nonzero"
                  d="M28.9700376,63.3244248 C47.6273373,55.1957357 60.0684594,49.8368063 66.2934036,47.2476366 C84.0668845,39.855031 87.7600616,38.5708563 90.1672227,38.528 C90.6966555,38.5191258 91.8804274,38.6503351 92.6472251,39.2725385 C93.294694,39.7979149 93.4728387,40.5076237 93.5580865,41.0057381 C93.6433345,41.5038525 93.7494885,42.63857 93.6651041,43.5252052 C92.7019529,53.6451182 88.5344133,78.2034783 86.4142057,89.5379542 C85.5170662,94.3339958 83.750571,95.9420841 82.0403991,96.0994568 C78.3237996,96.4414641 75.5015827,93.6432685 71.9018743,91.2836143 C66.2690414,87.5912212 63.0868492,85.2926952 57.6192095,81.6896017 C51.3004058,77.5256038 55.3966232,75.2369981 58.9976911,71.4967761 C59.9401076,70.5179421 76.3155302,55.6232293 76.6324771,54.2720454 C76.6721165,54.1030573 76.7089039,53.4731496 76.3346867,53.1405352 C75.9604695,52.8079208 75.4081573,52.921662 75.0095933,53.0121213 C74.444641,53.1403447 65.4461175,59.0880351 48.0140228,70.8551922 C45.4598218,72.6091037 43.1463059,73.4636682 41.0734751,73.4188859 C38.7883453,73.3695169 34.3926725,72.1268388 31.1249416,71.0646282 C27.1169366,69.7617838 23.931454,69.0729605 24.208838,66.8603276 C24.3533167,65.7078514 25.9403832,64.5292172 28.9700376,63.3244248 Z"
                />
              </svg>
            </a>
          </AppTooltip>
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

.preferences-panel__divider {
  height: 1px;
  margin: 0 -10px;
  background: var(--color-border-subtle);
}

.preferences-panel__mobile-navigation,
.preferences-panel__mobile-navigation-divider {
  display: none;
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

.preferences-panel__project-meta {
  display: flex;
  width: 100%;
  min-width: 0;
  align-items: center;
  justify-content: flex-start;
  gap: 0;
}

.preferences-panel__brand-link {
  display: inline-flex;
  width: 20px;
  height: 22px;
  flex: none;
  align-items: center;
  justify-content: flex-start;
}

.preferences-panel__brand-link--github {
  width: auto;
  color: light-dark(#181717, #f0f6fc);
}

.preferences-panel__brand-link--telegram {
  margin-left: auto;
  justify-content: flex-end;
}

.preferences-panel__brand-icon {
  width: 15px;
  height: 15px;
  flex: none;
}

.preferences-panel__star {
  flex: none;
  margin-left: 4px;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
  text-align: left;
  white-space: nowrap;
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
    width: min(280px, calc(100vw - 24px));
    min-width: min(244px, calc(100vw - 24px));
  }

  .preferences-panel__mobile-navigation {
    display: block;
  }

  .preferences-panel__mobile-navigation-divider {
    display: block;
    height: 1px;
    margin: 0 -10px;
    background: var(--color-border-subtle);
  }

  .preferences-panel label,
  .preferences-panel__action {
    min-height: var(--touch-target);
  }
}
</style>
