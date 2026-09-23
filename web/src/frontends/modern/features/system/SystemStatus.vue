<script setup lang="ts">
import { ArrowUpRight, Info, RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useMessages } from '@modern/app/messages'
import {
  AppExternalLink,
  AppIcon,
  AppIconButton,
  AppOverflowText,
  AppTooltip,
} from '@modern/components/ui'
import { useSystemStatus } from './useSystemStatus'

defineProps<{ collapsed?: boolean }>()

const { t } = useI18n()
const messages = useMessages()
const { version, versionLoading, checkState, update, checkForUpdate, canCheckUpdate } =
  useSystemStatus()
const versionLabel = computed(() =>
  version.value
    ? version.value.startsWith('v')
      ? version.value
      : `v${version.value}`
    : t(versionLoading.value ? 'system.loadingVersion' : 'system.versionUnavailable'),
)
const updateLabel = computed(() =>
  update.value ? t('system.updateAvailable', { version: update.value.version }) : '',
)
const checkLabel = computed(() =>
  t(checkState.value === 'checking' ? 'system.checking' : 'system.checkUpdate'),
)

async function handleCheckForUpdate(): Promise<void> {
  const result = await checkForUpdate()
  if (result === 'latest') {
    messages.show({ tone: 'success', text: t('system.latestVersion') })
  } else if (result === 'failed' || result === 'authRequired') {
    messages.show({
      tone: 'danger',
      text: t(result === 'failed' ? 'system.checkFailed' : 'system.authRequired'),
    })
  }
}
</script>

<template>
  <div class="modern-system-status" :class="{ 'is-compact': collapsed }">
    <div class="modern-version-row">
      <AppTooltip
        :label="t('system.currentVersion', { version: versionLabel })"
        :disabled="!collapsed"
        side="right"
      >
        <span class="modern-version" :tabindex="collapsed ? 0 : undefined">
          <AppIcon v-if="collapsed" :icon="Info" size="sm" />
          <AppOverflowText v-else :text="versionLabel" />
        </span>
      </AppTooltip>
      <AppIconButton
        v-if="canCheckUpdate"
        class="modern-update-button"
        :icon="RefreshCw"
        :label="checkLabel"
        size="xs"
        :loading="checkState === 'checking'"
        @click="handleCheckForUpdate"
      />
    </div>
    <AppTooltip v-if="update" :label="collapsed ? updateLabel : undefined">
      <AppExternalLink
        class="modern-update-release"
        :href="update.releaseURL"
        :aria-label="updateLabel"
      >
        <span v-if="!collapsed" class="modern-update-dot" aria-hidden="true" />
        <span v-if="!collapsed">{{ updateLabel }}</span>
        <AppIcon :icon="ArrowUpRight" size="xs" />
      </AppExternalLink>
    </AppTooltip>
  </div>
</template>

<style scoped>
.modern-system-status {
  border-top: var(--modern-line-width) solid var(--modern-border);
  margin-top: var(--modern-space-2);
  padding: var(--modern-space-2) var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}

.modern-version-row {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: center;
  gap: var(--modern-space-1);
}

.modern-version {
  min-width: 0;
  overflow: hidden;
  font-family: var(--modern-font-mono);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.modern-update-release {
  display: flex;
  min-height: var(--modern-control-xs);
  align-items: center;
  gap: var(--modern-space-1-5);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  margin-top: var(--modern-space-1);
  background: var(--modern-surface);
  padding: var(--modern-space-1) var(--modern-space-1-5);
  color: var(--modern-text);
  line-height: var(--modern-leading-compact);
}

.modern-update-dot {
  width: var(--modern-space-1);
  height: var(--modern-space-1);
  flex-shrink: 0;
  border-radius: var(--modern-radius-round);
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
  width: var(--modern-control-nav);
  min-height: var(--modern-control-sm);
  align-items: center;
  justify-content: center;
}

.is-compact .modern-update-release {
  border: 0;
  margin-inline: auto;
  background: transparent;
  padding: 0;
}

.is-compact .modern-update-release > svg {
  margin-left: 0;
}

@media (max-width: 760px) {
  .modern-update-release {
    min-height: var(--modern-touch-target);
  }
}
</style>
