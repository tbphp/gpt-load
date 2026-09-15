<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { SystemInfo } from '@modern/api/system'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppCopyValue,
  AppExternalLink,
} from '@modern/components/ui'
import { useSystemStatus } from '@modern/features/system/useSystemStatus'

defineProps<{ data?: SystemInfo; loading: boolean; failed: boolean }>()
defineEmits<{ retry: [] }>()
const { t } = useI18n()
const { checkState, update, checkForUpdate } = useSystemStatus()
const databaseNames = { sqlite: 'SQLite', mysql: 'MySQL', postgres: 'PostgreSQL' }
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
    <div class="modern-settings-version">
      <div>
        <span>{{ t('settingsForm.system.version') }}</span>
        <strong>{{ data.version }}</strong>
        <AppBadge size="xs">{{ t('settingsForm.system.deploymentValue') }}</AppBadge>
      </div>
      <AppButton
        :icon="RefreshCw"
        size="sm"
        :loading="checkState === 'checking'"
        @click="checkForUpdate"
        >{{ t('system.checkUpdate') }}</AppButton
      >
    </div>
    <p v-if="checkState === 'latest'" class="modern-settings-update" role="status">
      {{ t('system.latestVersion') }}
    </p>
    <p
      v-else-if="checkState === 'failed' || checkState === 'authRequired'"
      class="modern-settings-update is-error"
      role="alert"
    >
      {{ t(checkState === 'failed' ? 'system.checkFailed' : 'system.authRequired') }}
    </p>
    <AppExternalLink v-if="update" :href="update.releaseURL" class="modern-settings-update-link">{{
      t('system.updateAvailable', { version: update.version })
    }}</AppExternalLink>
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
      <div class="modern-settings-system-path">
        <dt>{{ t('settingsForm.system.dataDir') }}</dt>
        <dd><AppCopyValue :value="data.dataDir" /></dd>
      </div>
      <div
        v-for="key in ['authKey', 'encryption'] as const"
        :key="key"
        class="modern-settings-system-path"
      >
        <dt>{{ t('settingsForm.system.' + key + 'Source') }}</dt>
        <dd>
          <span>{{ t('settingsForm.system.sources.' + data[key].source) }}</span>
          <AppCopyValue v-if="data[key].path" :value="data[key].path!" />
        </dd>
      </div>
    </dl>
    <p v-if="failed" class="modern-settings-update is-error" role="status">
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
.modern-settings-version,
.modern-settings-version > div {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2) var(--modern-space-3);
}
.modern-settings-version {
  justify-content: space-between;
  padding-block: var(--modern-space-2);
}
.modern-settings-version span,
.modern-settings-system-facts dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-settings-version strong {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-settings-system-facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-4);
}
.modern-settings-system-facts > div {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: var(--modern-space-4);
}
.modern-settings-system-facts dt {
  flex: none;
}
.modern-settings-system-facts dd {
  display: flex;
  flex-wrap: wrap;
  min-width: 0;
  margin: 0;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-settings-system-path {
  grid-column: 1 / -1;
}
.modern-settings-update,
.modern-settings-update-link {
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
}
.modern-settings-update-link {
  width: fit-content;
  color: var(--modern-accent);
}
.modern-settings-update.is-error {
  color: var(--modern-danger);
}
@media (max-width: 760px) {
  .modern-settings-system-facts {
    grid-template-columns: minmax(0, 1fr);
  }
  .modern-settings-system-facts > div {
    flex-wrap: wrap;
    gap: var(--modern-space-1) var(--modern-space-3);
  }
}
</style>
