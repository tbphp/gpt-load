<script setup lang="ts">
import { Copy } from '@lucide/vue'
import {
  TooltipContent,
  TooltipPortal,
  TooltipProvider,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { canWriteToClipboardNatively, copyText } from '@/lib/clipboard'
import OverflowTooltip from './OverflowTooltip.vue'

const props = withDefaults(
  defineProps<{
    value: string
    label: string
    successLabel: string
    failureLabel: string
    resolveValue?: () => string | Promise<string>
    /**
     * leading：图标在值前面（默认）。trailing：图标跟在值后面。
     * icon：只渲染图标，用于值已由相邻标题承担的场景。
     */
    layout?: 'leading' | 'trailing' | 'icon'
  }>(),
  { resolveValue: undefined, layout: 'leading' },
)

type CopyState = 'idle' | 'success' | 'unsupported' | 'failure'

const { t } = useI18n()
const state = ref<CopyState>('idle')
let resetTimer: ReturnType<typeof setTimeout> | undefined

function scheduleReset(): void {
  if (resetTimer !== undefined) clearTimeout(resetTimer)
  resetTimer = setTimeout(() => {
    state.value = 'idle'
    resetTimer = undefined
  }, 2_000)
}

async function copyValue(): Promise<void> {
  if (props.resolveValue && !canWriteToClipboardNatively()) {
    state.value = 'unsupported'
    scheduleReset()
    return
  }

  try {
    const value = props.resolveValue ? await props.resolveValue() : props.value
    state.value = (await copyText(value)) ? 'success' : 'failure'
  } catch {
    state.value = 'failure'
  }
  scheduleReset()
}

onBeforeUnmount(() => {
  if (resetTimer !== undefined) clearTimeout(resetTimer)
})
</script>

<template>
  <span class="copy-chip-wrap">
    <TooltipProvider :delay-duration="0" :skip-delay-duration="0">
      <TooltipRoot
        :open="state !== 'idle'"
        :disable-hoverable-content="true"
        :disable-closing-trigger="true"
      >
        <TooltipTrigger as-child>
          <button
            class="copy-chip"
            :class="`copy-chip--${layout}`"
            :data-state="state"
            type="button"
            :aria-label="label"
            @click="copyValue"
          >
            <Copy v-if="layout === 'leading'" :size="14" aria-hidden="true" />
            <OverflowTooltip
              v-if="layout !== 'icon'"
              as="span"
              :content="value"
              :focusable="false"
              :tooltip-disabled="state !== 'idle'"
            >
              {{ value }}
            </OverflowTooltip>
            <Copy v-if="layout !== 'leading'" :size="14" aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipPortal>
          <TooltipContent
            class="copy-chip__feedback"
            :class="`copy-chip__feedback--${state}`"
            side="bottom"
            align="start"
            :side-offset="7"
            :collision-padding="8"
            role="status"
            aria-live="polite"
          >
            {{
              state === 'unsupported'
                ? t('common.copyUnsupported')
                : state === 'success'
                  ? successLabel
                  : failureLabel
            }}
          </TooltipContent>
        </TooltipPortal>
      </TooltipRoot>
    </TooltipProvider>
  </span>
</template>

<style scoped>
.copy-chip-wrap {
  position: relative;
  display: inline-flex;
  width: auto;
  max-width: 100%;
  min-width: 0;
}

.copy-chip {
  display: inline-flex;
  width: auto;
  max-width: 100%;
  min-width: 0;
  min-height: var(--control-compact);
  align-items: center;
  justify-content: flex-start;
  gap: 7px;
  border: 0;
  border-radius: var(--radius-tag);
  appearance: none;
  background: transparent;
  color: var(--color-text-faint);
  padding: 3px 0;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  cursor: pointer;
  transition:
    color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.copy-chip span {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.copy-chip svg {
  flex: none;
}

/* 图标模式没有文字撑开点击区，补足到与其他紧凑控件一致的尺寸。 */
.copy-chip--icon {
  width: var(--control-compact);
  min-width: var(--control-compact);
  justify-content: center;
  padding: 0;
}

.copy-chip:hover,
.copy-chip[data-state='unsupported'],
.copy-chip[data-state='success'],
.copy-chip[data-state='failure'] {
  background: var(--color-surface-sunken);
  color: var(--color-action);
}

.copy-chip[data-state='success'] {
  color: var(--color-success);
}

.copy-chip[data-state='unsupported'],
.copy-chip[data-state='failure'] {
  color: var(--color-danger);
}

@media (max-width: 860px) {
  .copy-chip {
    min-height: var(--touch-target);
  }
}
</style>

<style>
.copy-chip__feedback {
  z-index: var(--z-popover);
  border: 1px solid var(--color-feedback-success-border);
  border-radius: var(--radius-tag);
  background: var(--color-surface);
  box-shadow: var(--shadow-feedback);
  color: var(--color-success);
  padding: 5px 7px;
  font-size: var(--text-sm);
  pointer-events: none;
  transform-origin: var(--reka-tooltip-content-transform-origin);
  white-space: nowrap;
}

.copy-chip__feedback--unsupported,
.copy-chip__feedback--failure {
  border-color: var(--color-feedback-danger-border);
  color: var(--color-danger);
}

.copy-chip__feedback[data-state='delayed-open'],
.copy-chip__feedback[data-state='instant-open'] {
  animation: copy-chip-feedback var(--duration-fast) var(--easing-standard);
}

@keyframes copy-chip-feedback {
  from {
    opacity: 0;
    transform: translateY(-3px);
  }
}

@media (prefers-reduced-motion: reduce) {
  .copy-chip__feedback[data-state='delayed-open'],
  .copy-chip__feedback[data-state='instant-open'] {
    animation: none;
  }
}
</style>
