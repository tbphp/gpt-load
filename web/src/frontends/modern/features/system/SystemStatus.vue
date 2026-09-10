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
          :size="13"
          :class="{ 'is-spinning': checkState === 'checking' }"
          aria-hidden="true"
        />
        <span v-if="!collapsed">{{ checkLabel }}</span>
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
      <span v-if="!collapsed">{{ statusMessage }}</span>
      <ArrowUpRight :size="14" aria-hidden="true" />
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
  margin-top: 10px;
  padding: 12px 10px 16px;
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
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.modern-update-button {
  display: inline-flex;
  min-height: 30px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  gap: 5px;
  border: 0;
  border-radius: 4px;
  background: transparent;
  padding: 4px;
  color: inherit;
  font-size: 11px;
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
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding-top: 6px;
  color: var(--modern-accent);
}

.modern-update-release:hover {
  text-decoration: underline;
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

.is-spinning {
  animation: modern-update-spin 1s linear infinite;
}

@keyframes modern-update-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
