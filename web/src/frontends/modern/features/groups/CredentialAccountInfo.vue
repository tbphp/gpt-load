<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import { credentialTime } from './credential-presentation'

const props = defineProps<{ row: CredentialRow }>()
const { t, locale } = useI18n()
const fields = computed(() => {
  const row = props.row
  const observation = row.observation
  const account = observation?.account
  return [
    ['organization', observation?.organization],
    ['seat', account?.seat],
    ['billing', account?.billing],
    ['organizationRole', account?.organizationRole],
    ['workspaceRole', account?.workspaceRole],
    ['rateLimitTier', account?.rateLimitTier],
    [
      'extraUsage',
      account?.extraUsage === undefined
        ? undefined
        : t(account.extraUsage ? 'groupDetail.on' : 'groupDetail.off'),
    ],
    ['expiresAt', credentialTime(row.expiresAt, locale.value)],
    ['lastRefresh', credentialTime(row.lastRefresh, locale.value)],
    [
      'quotaUpdated',
      observation?.observedAt ? credentialTime(observation.observedAt, locale.value) : undefined,
    ],
    ['lastUsed', credentialTime(row.lastUsed, locale.value)],
    ['errorCode', row.authError || observation?.error],
  ].filter((field): field is [string, string] => Boolean(field[1]))
})
</script>
<template>
  <dl class="modern-credential-account-info">
    <div v-for="[key, value] in fields" :key="key">
      <dt>{{ t('credentialCards.' + key) }}</dt>
      <dd>{{ value }}</dd>
    </div>
  </dl>
</template>
<style scoped>
.modern-credential-account-info {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-3);
}
.modern-credential-account-info > div {
  min-width: 0;
}
.modern-credential-account-info dt {
  margin-bottom: var(--modern-space-0-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-credential-account-info dd {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--modern-text);
  font-size: var(--modern-font-size-small);
}
@container (max-width: 300px) {
  .modern-credential-account-info {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
