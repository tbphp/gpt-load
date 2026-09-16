<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HomeAccount } from '@modern/api/home'
import type { CredentialQuota } from '@modern/api/credential-observation'
import {
  AppButton,
  AppOverflowText,
  AppPanel,
  AppProgressBar,
  AppTooltip,
} from '@modern/components/ui'
import {
  credentialStatus,
  credentialTime,
  quotaRemaining,
  quotaTone,
  quotaWindowTitle,
} from '@modern/features/groups/credential-presentation'
import HomeSectionLink from './HomeSectionLink.vue'

const props = defineProps<{ accounts: HomeAccount[]; failed: boolean; loading: boolean }>()
defineEmits<{ retry: [] }>()
const { t, te, n, locale } = useI18n()
const remaining = (window: CredentialQuota) =>
  window.state === 'exhausted' ? 0 : quotaRemaining(window)
function tightest(windows: readonly CredentialQuota[]): CredentialQuota | undefined {
  return windows.reduce<CredentialQuota | undefined>((tight, window) => {
    const percent = remaining(window)
    if (percent === undefined) return tight
    return !tight || percent < remaining(tight)! ? window : tight
  }, undefined)
}
function windowTitle(window: CredentialQuota): string {
  const key = 'credentialCards.quotaLabels.' + (window.labelKey || window.label.toLowerCase())
  return quotaWindowTitle(window, te(key) ? t(key) : window.label)
}
function resetLabel(window?: CredentialQuota): string | undefined {
  if (!window?.resetsAt || remaining(window) !== 0) return undefined
  const minutes = Math.ceil((window.resetsAt - Date.now()) / 60000)
  if (minutes <= 0) return t('credentialCards.windowExpired')
  const hours = Math.floor(minutes / 60)
  const duration =
    hours >= 24
      ? ([
          [Math.floor(hours / 24), 'day'],
          [hours % 24, 'hour'],
        ] as const)
      : ([
          [hours, 'hour'],
          [minutes % 60, 'minute'],
        ] as const)
  return t('home.recoversIn', {
    time: duration
      .filter(([value]) => value > 0)
      .map(([value, unit]) => n(value, { style: 'unit', unit, unitDisplay: 'short' }))
      .join(' '),
  })
}
const rows = computed(() =>
  props.accounts.map((account) => {
    const observation = account.credential.observation
    const window = tightest(observation?.windows ?? [])
    const quota = window ? remaining(window) : undefined
    const used = quota === undefined ? undefined : 100 - quota
    const status = credentialStatus(account.credential)
    const hint = [
      ...(observation?.windows ?? []).map((window) => {
        const value = remaining(window)
        return [
          windowTitle(window),
          value === undefined
            ? t('home.noQuota')
            : t('home.quotaUsed', { percent: n(100 - value, { maximumFractionDigits: 1 }) }),
          window.resetsAt
            ? t('credentialCards.resetsAt', { time: credentialTime(window.resetsAt, locale.value) })
            : undefined,
        ]
          .filter(Boolean)
          .join(' · ')
      }),
      status.tone !== 'success' ? t(status.key) : undefined,
      observation && observation.state !== 'fresh'
        ? t('credentialCards.observation.' + observation.state)
        : undefined,
      observation?.resetCredits
        ? t('credentialCards.resetCredits', { count: n(observation.resetCredits) })
        : undefined,
    ]
      .filter(Boolean)
      .join('\n')
    return {
      id: account.credential.id,
      channelID: account.channelID,
      name: account.credential.account || account.credential.mask || account.channelName,
      plan: observation?.plan || account.channelName,
      used,
      tone: window ? quotaTone(window) : ('neutral' as const),
      hint,
      reset: resetLabel(window),
    }
  }),
)
</script>

<template>
  <AppPanel :title="t('home.activeAccounts')" compact>
    <template #actions>
      <HomeSectionLink
        :to="{ name: 'modern-groups', query: { connection: 'subscription' } }"
        :label="t('home.all')"
        :arrow="false"
      />
    </template>
    <div v-if="failed" class="modern-home-account-state" role="status">
      <span>{{ t(accounts.length ? 'home.refreshFailed' : 'home.accountsFailed') }}</span>
      <AppButton size="xs" variant="text" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-if="!accounts.length && !failed" class="modern-home-account-state">
      {{ t(loading ? 'ui.loading' : 'home.noRecentAccounts') }}
    </p>
    <ul v-if="accounts.length" class="modern-home-accounts">
      <li v-for="row in rows" :key="row.id">
        <RouterLink
          :to="{ name: 'modern-groups', query: { channel: row.channelID } }"
          class="modern-home-account-head"
        >
          <AppOverflowText class="modern-home-account-name" :text="row.name" />
          <AppOverflowText class="modern-home-account-plan" :text="row.plan" />
          <span
            class="modern-home-account-percent"
            :class="{ 'is-tight': row.tone === 'danger' }"
            >{{ row.used === undefined ? '—' : n(Math.round(row.used)) + '%' }}</span
          >
        </RouterLink>
        <AppTooltip v-if="row.used !== undefined" :label="row.hint">
          <AppProgressBar
            :label="row.name + ' · ' + t('home.quotaUsed', { percent: n(Math.round(row.used)) })"
            :value="row.used"
            :tone="row.tone"
            size="sm"
          />
        </AppTooltip>
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
.modern-home-account-name {
  flex: 0 1 auto;
  max-width: 52%;
}
.modern-home-account-head:hover .modern-home-account-name {
  color: var(--modern-accent);
  text-decoration: underline;
  text-underline-offset: var(--modern-space-1);
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
