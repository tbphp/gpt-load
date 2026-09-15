<script setup lang="ts">
import { RefreshCw } from '@lucide/vue'
import { computed } from 'vue'
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
// 三种更新状态合成一行，避免版本下方堆三段样式各异的提示。
const updateMessage = computed(() => {
  if (update.value)
    return { text: t('system.updateAvailable', { version: update.value.version }), error: false }
  if (checkState.value === 'latest') return { text: t('system.latestVersion'), error: false }
  if (checkState.value === 'failed') return { text: t('system.checkFailed'), error: true }
  if (checkState.value === 'authRequired') return { text: t('system.authRequired'), error: true }
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
  <dl v-else class="modern-settings-system">
    <div class="modern-settings-system-version">
      <dt>{{ t('settingsForm.system.version') }}</dt>
      <dd>
        <strong>{{ data.version }}</strong>
        <AppBadge size="xs">{{ t('settingsForm.system.deploymentValue') }}</AppBadge>
        <AppButton
          :icon="RefreshCw"
          size="xs"
          :loading="checkState === 'checking'"
          @click="checkForUpdate"
          >{{ t('system.checkUpdate') }}</AppButton
        >
      </dd>
    </div>
    <div v-if="updateMessage" class="modern-settings-system-note">
      <dt></dt>
      <dd :class="{ 'is-error': updateMessage.error }">
        <AppExternalLink v-if="update" :href="update.releaseURL">{{
          updateMessage.text
        }}</AppExternalLink>
        <span v-else :role="updateMessage.error ? 'alert' : 'status'">{{
          updateMessage.text
        }}</span>
      </dd>
    </div>
    <div>
      <dt>{{ t('settingsForm.system.database') }}</dt>
      <dd>{{ databaseNames[data.database] }}</dd>
    </div>
    <div>
      <dt>{{ t('settingsForm.system.encryption') }}</dt>
      <dd>
        <AppBadge size="xs" tone="info">{{ t('settingsForm.system.encryptionEnabled') }}</AppBadge>
      </dd>
    </div>
    <div>
      <dt>{{ t('settingsForm.system.dataDir') }}</dt>
      <dd><AppCopyValue :value="data.dataDir" /></dd>
    </div>
    <div v-for="key in ['authKey', 'encryption'] as const" :key="key">
      <dt>{{ t('settingsForm.system.' + key + 'Source') }}</dt>
      <dd>
        <span>{{ t('settingsForm.system.sources.' + data[key].source) }}</span>
        <AppCopyValue v-if="data[key].path" :value="data[key].path!" />
      </dd>
    </div>
    <div v-if="failed" class="modern-settings-system-note">
      <dt></dt>
      <dd class="is-error" role="status">{{ t('settingsForm.stale') }}</dd>
    </div>
  </dl>
</template>

<style scoped>
/* 标签列定宽右对齐：原先 dt 用 flex:none 按各自文字宽度收缩，
   「数据库」和「管理员密钥来源」差了一倍，取值起点全部错开。 */
.modern-settings-system {
  display: grid;
  min-width: 0;
  margin: 0;
  gap: var(--modern-space-1);
}
.modern-settings-system > div {
  display: grid;
  grid-template-columns: 104px minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-3);
  min-width: 0;
  min-height: var(--modern-space-8);
}
.modern-settings-system dt {
  overflow-wrap: break-word;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  text-align: right;
}
.modern-settings-system dd {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  margin: 0;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-settings-system-version strong {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
/* 检查更新与版本同排，推到右端。 */
.modern-settings-system-version dd > :last-child {
  margin-inline-start: auto;
}
.modern-settings-system-note {
  min-height: 0;
}
.modern-settings-system-note dd {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-settings-system-note dd.is-error {
  color: var(--modern-danger);
}
@media (max-width: 760px) {
  .modern-settings-system > div {
    grid-template-columns: minmax(0, 1fr);
    align-items: start;
    gap: var(--modern-space-1);
  }
  .modern-settings-system dt {
    text-align: left;
  }
  .modern-settings-system-note dt {
    display: none;
  }
}
</style>
