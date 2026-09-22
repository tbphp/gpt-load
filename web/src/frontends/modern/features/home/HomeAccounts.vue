<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HomeAccount } from '@modern/api/home'
import type { CredentialQuota } from '@modern/api/credential-observation'
import type { GroupRow } from '@modern/api/groups'
import {
  AppButton,
  AppChannelIcon,
  AppOverflowText,
  AppPanel,
  AppProgressBar,
  AppTooltip,
} from '@modern/components/ui'
import { useClock } from '@modern/components/ui/clock'
import { formatRemainingDuration } from '@modern/components/ui/format'
import {
  quotaCycleTime,
  quotaRemaining,
  quotaTone,
  quotaWindowRange,
  quotaWindowTitle,
  sortedQuotaWindows,
} from '@modern/features/groups/credential-presentation'

const props = defineProps<{
  accounts: HomeAccount[]
  groups: ReadonlyMap<number, GroupRow>
  failed: boolean
  loading: boolean
}>()
defineEmits<{ retry: [] }>()
const { t, te, n, locale } = useI18n()
const now = useClock()
const remaining = (window: CredentialQuota) =>
  window.state === 'exhausted' ? 0 : quotaRemaining(window)
function tightest(windows: readonly CredentialQuota[]): CredentialQuota | undefined {
  return windows.reduce<CredentialQuota | undefined>((tight, window) => {
    const percent = remaining(window)
    if (percent === undefined) return tight
    return !tight || percent < remaining(tight)! ? window : tight
  }, undefined)
}
function resetLabel(window?: CredentialQuota): string | undefined {
  if (!window?.resetsAt || remaining(window) !== 0) return undefined
  const remainingMs = window.resetsAt - now.value
  if (remainingMs <= 0) return t('credentialCards.windowExpired')
  return t('home.recoversIn', {
    time: formatRemainingDuration(remainingMs, locale.value),
  })
}
function windowHint(window: CredentialQuota): string {
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  const name = quotaWindowTitle(window, window.labelKey && te(key) ? t(key) : window.label)
  const range = quotaWindowRange(window)
  const percent = remaining(window)
  return [
    `${name || window.label || t('credentialCards.window')} - ${percent === undefined ? '—' : n(Math.round(percent)) + '%'}`,
    range
      ? `${quotaCycleTime(range.start)} – ${quotaCycleTime(range.end)}`
      : quotaCycleTime(window.resetsAt),
  ].join('\n')
}
const rows = computed(() =>
  props.accounts.map((account) => {
    const observation = account.credential.observation
    const windows = sortedQuotaWindows(observation?.windows ?? [])
    const window = tightest(windows)
    const quota = window ? remaining(window) : undefined
    return {
      key: account.key,
      to: {
        name: account.groups === 1 && account.groupID ? 'modern-group-detail' : 'modern-groups',
        ...(account.groups === 1 && account.groupID ? { params: { id: account.groupID } } : {}),
        query: { credential_key: account.key },
      },
      name: account.credential.account || account.channelName,
      channelIcon: account.channelIcon,
      channelMark: account.channelMark,
      channelName: account.channelName,
      groupName: account.groupID ? props.groups.get(account.groupID)?.name : undefined,
      plan: observation?.plan || account.channelName,
      windows,
      remaining: quota,
      tone: window ? quotaTone(window) : ('neutral' as const),
      reset: resetLabel(window),
    }
  }),
)
</script>

<template>
  <AppPanel :title="t('home.activeAccounts')" compact>
    <div v-if="failed" class="modern-home-account-state" role="status">
      <span>{{ t(accounts.length ? 'home.refreshFailed' : 'home.accountsFailed') }}</span>
      <AppButton size="xs" variant="text" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-if="!accounts.length && !failed" class="modern-home-account-state">
      {{ t(loading ? 'ui.loading' : 'home.noRecentAccounts') }}
    </p>
    <ul v-if="accounts.length" class="modern-home-accounts">
      <li v-for="row in rows" :key="row.key">
        <div class="modern-home-account-head">
          <AppChannelIcon
            :icon="row.channelIcon"
            :mark="row.channelMark"
            :name="row.channelName"
            :group-name="row.groupName"
            size="sm"
            class="modern-home-account-channel"
          />
          <AppButton as-child variant="text" size="xs" class="modern-home-account-name">
            <RouterLink :to="row.to"><AppOverflowText :text="row.name" /></RouterLink>
          </AppButton>
          <AppOverflowText class="modern-home-account-plan" :text="row.plan" />
          <span
            class="modern-home-account-percent"
            :class="{ 'is-tight': row.tone === 'danger' }"
            >{{ row.remaining === undefined ? '—' : n(Math.round(row.remaining)) + '%' }}</span
          >
        </div>
        <div
          v-if="row.windows.length"
          class="modern-home-account-quotas"
          role="group"
          :aria-label="row.name"
        >
          <AppTooltip v-for="window in row.windows" :key="window.id" :label="windowHint(window)">
            <AppProgressBar
              :label="windowHint(window)"
              :value="remaining(window)"
              :tone="quotaTone(window)"
              size="sm"
              tabindex="0"
            />
          </AppTooltip>
        </div>
        <span v-else class="modern-home-account-note">{{ t('home.noQuota') }}</span>
        <span v-if="row.reset" class="modern-home-account-note">{{ row.reset }}</span>
      </li>
    </ul>
  </AppPanel>
</template>

<style scoped>
.modern-home-account-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-home-accounts {
  display: grid;
  gap: var(--modern-space-3);
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-home-account-state + .modern-home-accounts {
  margin-top: var(--modern-space-3);
}
.modern-home-accounts > li {
  display: grid;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-home-account-head {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-1);
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-account-channel {
  align-self: center;
}
.modern-home-account-quotas {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: minmax(0, 1fr);
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-home-account-name {
  flex: 0 1 auto;
  max-width: 52%;
  min-width: 0;
}
.modern-home-account-plan {
  flex: 1;
  color: var(--modern-muted);
}
.modern-home-account-percent {
  flex: none;
  margin-inline-start: auto;
  color: var(--modern-muted);
  font-variant-numeric: tabular-nums;
}
.modern-home-account-percent.is-tight {
  color: var(--modern-danger);
}
.modern-home-account-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
</style>
