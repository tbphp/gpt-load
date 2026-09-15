<script setup lang="ts">
import { ArrowUpRight, RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { SystemInfo } from '@modern/api/system'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppCopyValue,
  AppExternalLink,
  AppIcon,
} from '@modern/components/ui'
import { useSystemStatus } from '@modern/features/system/useSystemStatus'

defineProps<{ data?: SystemInfo; loading: boolean; failed: boolean }>()
defineEmits<{ retry: [] }>()
const { t } = useI18n()
const { checkState, update, checkForUpdate } = useSystemStatus()
const databaseNames = { sqlite: 'SQLite', mysql: 'MySQL', postgres: 'PostgreSQL' }
const updateMessage = computed(() => {
  if (checkState.value === 'failed') return { text: t('system.checkFailed'), error: true }
  if (checkState.value === 'authRequired') return { text: t('system.authRequired'), error: true }
  if (checkState.value === 'latest') return { text: t('system.latestVersion'), error: false }
  return undefined
})
</script>

<template>
  <AppCollectionState
    v-if="!data"
    :loading="loading"
    :error="failed"
    :title="t(loading ? 'settingsForm.loading' : 'settingsForm.system.failed')"
  >
    <AppButton v-if="failed" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
  </AppCollectionState>
  <div v-else class="modern-settings-system">
    <div class="modern-settings-system-release">
      <div class="modern-settings-system-version">
        <span>{{ t('settingsForm.system.version') }}</span>
        <strong>{{ data.version }}</strong>
        <AppBadge size="xs">{{ t('settingsForm.system.deploymentValue') }}</AppBadge>
      </div>
      <div class="modern-settings-system-actions">
        <div class="modern-settings-system-update" aria-live="polite">
          <AppExternalLink
            v-if="update"
            :href="update.releaseURL"
            class="modern-settings-system-release-link"
          >
            <AppIcon :icon="ArrowUpRight" size="sm" />
            <span>{{ t('system.updateAvailable', { version: update.version }) }}</span>
          </AppExternalLink>
        </div>
        <AppButton
          :icon="RefreshCw"
          size="sm"
          :loading="checkState === 'checking'"
          @click="checkForUpdate"
          >{{ t('system.checkUpdate') }}</AppButton
        >
      </div>
    </div>
    <p
      v-if="updateMessage"
      class="modern-settings-system-note"
      :class="{ 'is-error': updateMessage.error }"
      :role="updateMessage.error ? 'alert' : 'status'"
    >
      {{ updateMessage.text }}
    </p>
    <dl class="modern-settings-system-facts">
      <div>
        <dt>{{ t('settingsForm.system.database') }}</dt>
        <dd>{{ databaseNames[data.database] }}</dd>
      </div>
      <div>
        <dt>{{ t('settingsForm.system.encryption') }}</dt>
        <dd>
          <AppBadge size="xs" tone="info">{{
            t('settingsForm.system.encryptionEnabled')
          }}</AppBadge>
        </dd>
      </div>
      <div class="modern-settings-system-wide">
        <dt>{{ t('settingsForm.system.dataDir') }}</dt>
        <dd><AppCopyValue :value="data.dataDir" /></dd>
      </div>
      <div
        v-for="key in ['authKey', 'encryption'] as const"
        :key="key"
        class="modern-settings-system-wide"
      >
        <dt>{{ t('settingsForm.system.' + key + 'Source') }}</dt>
        <dd>
          <AppBadge size="xs">{{ t('settingsForm.system.sources.' + data[key].source) }}</AppBadge>
          <AppCopyValue v-if="data[key].path" :value="data[key].path!" />
        </dd>
      </div>
    </dl>
    <p v-if="failed" class="modern-settings-system-note is-error" role="status">
      {{ t('settingsForm.stale') }}
    </p>
  </div>
</template>

<style scoped>
.modern-settings-system {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-3);
}
.modern-settings-system-release,
.modern-settings-system-version {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  min-width: 0;
  gap: var(--modern-space-2) var(--modern-space-3);
}
.modern-settings-system-release {
  justify-content: space-between;
}
.modern-settings-system-version > span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-settings-system-version strong {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  overflow-wrap: anywhere;
}
.modern-settings-system-actions {
  display: flex;
  align-items: center;
  min-width: 0;
  max-width: 100%;
  margin-inline-start: auto;
  gap: var(--modern-space-2);
}
.modern-settings-system-update {
  min-width: 0;
}
.modern-settings-system-release-link {
  display: flex;
  align-items: center;
  min-height: var(--modern-control-sm);
  gap: var(--modern-space-1);
  border: var(--modern-line-width) solid transparent;
  border-radius: var(--modern-radius-control);
  background: var(--modern-badge-brand-surface);
  padding: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-accent);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
  text-decoration: none;
}
.modern-settings-system-release-link > span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-settings-system-release-link:hover {
  border-color: var(--modern-accent);
  background: var(--modern-accent-soft);
}
.modern-settings-system-release-link:is(:hover, :focus-visible) > span {
  text-decoration: underline;
  text-underline-offset: var(--modern-space-1);
}
.modern-settings-system-release-link:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
  box-shadow: var(--modern-shadow-focus);
}
.modern-settings-system-facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: var(--modern-space-5);
  min-width: 0;
  margin: 0;
}
.modern-settings-system-facts > div {
  display: grid;
  grid-template-columns: 144px minmax(0, 1fr);
  align-items: start;
  gap: var(--modern-space-3);
  min-width: 0;
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-block: var(--modern-space-3);
}
.modern-settings-system-facts dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  overflow-wrap: anywhere;
}
.modern-settings-system-facts dd {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  margin: 0;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-settings-system-wide {
  grid-column: 1 / -1;
}
.modern-settings-system-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-settings-system-note.is-error {
  color: var(--modern-danger);
}
@container modern-settings-content (max-width: 700px) {
  .modern-settings-system-facts {
    grid-template-columns: minmax(0, 1fr);
  }
}
@container modern-settings-content (max-width: 440px) {
  .modern-settings-system-facts > div {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--modern-space-1);
  }
}
</style>
