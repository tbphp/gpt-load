<script setup lang="ts">
import { ArrowUpRight, Info, RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import HintTooltip from '@modern/components/HintTooltip.vue'
import { useSystemStatus } from './useSystemStatus'

defineProps<{ collapsed?: boolean }>()

const { t } = useI18n()
const { version, versionLoading, checkState, update, checkForUpdate } = useSystemStatus()
const versionLabel = computed(() =>
  version.value
    ? version.value.startsWith('v')
      ? version.value
      : `v${version.value}`
    : t(versionLoading.value ? 'system.loadingVersion' : 'system.versionUnavailable'),
)
const statusMessage = computed(() => {
  if (update.value) return t('system.updateAvailable', { version: update.value.version })
  if (checkState.value === 'latest') return t('system.latestVersion')
  if (checkState.value === 'failed') return t('system.checkFailed')
  if (checkState.value === 'authRequired') return t('system.authRequired')
  return ''
})
const checkLabel = computed(() =>
  t(checkState.value === 'checking' ? 'system.checking' : 'system.checkUpdate'),
)
</script>

<template>
  <div class="modern-system-status" :class="{ 'is-compact': collapsed }">
    <div class="modern-version-row">
      <HintTooltip
        :label="t('system.currentVersion', { version: versionLabel })"
        :disabled="!collapsed"
        side="right"
      >
        <span
          class="modern-version"
          :title="t('system.currentVersion', { version: versionLabel })"
          :tabindex="collapsed ? 0 : undefined"
        >
          <Info v-if="collapsed" :size="15" aria-hidden="true" />
          <template v-else>{{ versionLabel }}</template>
        </span>
      </HintTooltip>
      <button
        class="modern-update-button"
        type="button"
        :aria-label="checkLabel"
        :title="collapsed && statusMessage ? statusMessage : checkLabel"
        :disabled="checkState === 'checking'"
        @click="checkForUpdate"
      >
        <RefreshCw
          :size="14"
          :class="{ 'is-spinning': checkState === 'checking' }"
          aria-hidden="true"
        />
      </button>
    </div>
    <a
      v-if="update"
      class="modern-update-release"
      :href="update.releaseURL"
      target="_blank"
      rel="noopener noreferrer"
      :title="statusMessage"
      :aria-label="statusMessage"
    >
      <span v-if="!collapsed" class="modern-update-dot" aria-hidden="true" />
      <span v-if="!collapsed">{{ statusMessage }}</span>
      <ArrowUpRight :size="12" aria-hidden="true" />
    </a>
    <p
      v-else-if="statusMessage"
      class="modern-update-status"
      :class="{ 'is-error': checkState === 'failed', 'modern-sr-only': collapsed }"
      :role="checkState === 'failed' ? 'alert' : 'status'"
    >
      {{ statusMessage }}
    </p>
  </div>
</template>

<style scoped>
.modern-system-status {
  border-top: 1px solid var(--modern-border);
  margin-top: 8px;
  padding: 8px 8px 12px;
  color: var(--modern-muted);
  font-size: 11px;
}

.modern-version-row {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.modern-version {
  min-width: 0;
  overflow: hidden;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.modern-update-button {
  display: inline-flex;
  width: 28px;
  height: 28px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 6px;
  background: transparent;
  padding: 4px;
  color: inherit;
}

.modern-update-button:hover:not(:disabled) {
  background: var(--modern-surface);
  color: var(--modern-text);
}

.modern-update-button:disabled {
  cursor: progress;
}

.modern-update-release {
  display: flex;
  min-height: 28px;
  align-items: center;
  gap: 6px;
  border: 1px solid var(--modern-border);
  border-radius: 6px;
  margin-top: 4px;
  background: var(--modern-surface);
  padding: 5px 7px;
  color: var(--modern-text);
  line-height: 1.4;
}

.modern-update-dot {
  width: 4px;
  height: 4px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--modern-coral);
}

.modern-update-release > svg {
  flex-shrink: 0;
  margin-left: auto;
  color: var(--modern-muted);
}

.modern-update-release:hover {
  border-color: var(--modern-muted);
}

.modern-update-status {
  margin-top: 6px;
  line-height: 1.5;
}

.modern-update-status.is-error {
  color: var(--modern-accent);
}

.is-compact {
  padding-inline: 0;
}

.is-compact .modern-version-row {
  flex-direction: column;
  gap: 0;
}

.is-compact .modern-version,
.is-compact .modern-update-button,
.is-compact .modern-update-release {
  display: flex;
  width: 40px;
  min-height: 32px;
  align-items: center;
  justify-content: center;
}

.is-compact .modern-update-release {
  border: 0;
  background: transparent;
  padding: 0;
}

.is-compact .modern-update-release > svg {
  margin-left: 0;
}

@media (max-width: 760px) {
  .modern-update-button {
    width: 44px;
    height: 44px;
  }

  .modern-update-release {
    min-height: 44px;
  }
}

.is-spinning {
  animation: modern-update-spin 1s linear infinite;
}

@keyframes modern-update-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
