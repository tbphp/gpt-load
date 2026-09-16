<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HomeAccount } from '@modern/api/home'
import type { CredentialQuota } from '@modern/api/credential-observation'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppIcon,
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

const props = defineProps<{ accounts: HomeAccount[]; failed: boolean; loading: boolean }>()
defineEmits<{ retry: [] }>()
const { t, te, n, locale } = useI18n()
function tightest(windows: readonly CredentialQuota[]): CredentialQuota | undefined {
  return windows.reduce<CredentialQuota | undefined>((tight, window) => {
    const percent = quotaRemaining(window)
    if (percent === undefined) return tight
    const current = tight ? quotaRemaining(tight) : undefined
    return current === undefined || percent < current ? window : tight
  }, undefined)
}
function windowTitle(window: CredentialQuota): string {
  const key = 'credentialCards.quotaLabels.' + (window.labelKey || window.label.toLowerCase())
  return quotaWindowTitle(window, te(key) ? t(key) : window.label)
}
function quotaHint(windows: readonly CredentialQuota[]): string {
  return windows
    .map((window) => {
      const percent = quotaRemaining(window)
      return [
        windowTitle(window),
        percent === undefined
          ? t('home.noQuota')
          : t('credentialCards.remaining', {
              value: n(percent, { maximumFractionDigits: 1 }) + '%',
            }),
        window.resetsAt
          ? t('credentialCards.resetsAt', { time: credentialTime(window.resetsAt, locale.value) })
          : undefined,
      ]
        .filter(Boolean)
        .join(' · ')
    })
    .join('\n')
}
const rows = computed(() =>
  props.accounts.map((account) => {
    const observation = account.credential.observation
    const window = observation ? tightest(observation.windows) : undefined
    const status = credentialStatus(account.credential)
    const tone = window ? quotaTone(window) : 'neutral'
    return {
      account,
      id: account.credential.id,
      name: account.credential.account || account.credential.mask || account.channelName,
      plan: observation?.plan || account.channelName,
      title: window ? windowTitle(window) : '',
      hint: observation ? quotaHint(observation.windows) : '',
      percent: window ? quotaRemaining(window) : undefined,
      tone: tone === 'success' ? ('info' as const) : tone,
      resetsAt: window && (tone === 'warning' || tone === 'danger') ? window.resetsAt : undefined,
      credits: observation?.resetCredits ?? 0,
      status: status.tone === 'success' ? undefined : status,
      observationState:
        observation && observation.state !== 'fresh' ? observation.state : undefined,
    }
  }),
)
function creditHint(row: (typeof rows.value)[number]): string {
  return (
    row.account.credential.observation?.creditExpirations
      .map((time, index) =>
        t('credentialCards.creditExpiry', {
          index: n(index + 1),
          time: time ? credentialTime(time, locale.value) : t('credentialCards.noExpiry'),
        }),
      )
      .join('\n') || t('credentialCards.resetCredits', { count: n(row.credits) })
  )
}
</script>

<template>
  <AppPanel :title="t('home.activeAccounts')" :description="t('home.recentAccounts')" compact>
    <template #actions>
      <AppButton as-child size="xs" variant="text">
        <RouterLink :to="{ name: 'modern-groups', query: { connection: 'subscription' } }">
          {{ t('home.viewAll') }}<AppIcon :icon="ArrowRight" size="sm" />
        </RouterLink>
      </AppButton>
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
          :to="{ name: 'modern-groups', query: { channel: row.account.channelID } }"
          class="modern-home-account-link"
        >
          <span class="modern-home-account-head">
            <AppChannelIcon
              :icon="row.account.channelIcon"
              :name="row.account.channelName"
              :mark="row.account.channelMark"
              size="sm"
              :tooltip="false"
            />
            <AppOverflowText class="modern-home-account-name" :text="row.name" />
            <span
              v-if="row.percent !== undefined"
              class="modern-home-account-percent"
              :data-tone="row.tone"
            >
              {{ n(Math.round(row.percent)) }}%
            </span>
          </span>
          <span class="modern-home-account-meta">
            <AppOverflowText class="modern-home-account-plan" :text="row.plan" />
            <AppBadge v-if="row.status" :tone="row.status.tone" size="xs" variant="plain" dot>
              {{ t(row.status.key) }}
            </AppBadge>
            <span v-else-if="row.percent !== undefined">{{
              t('credentialCards.remainingShort')
            }}</span>
          </span>
          <AppTooltip v-if="row.percent !== undefined" :label="row.hint">
            <AppProgressBar
              :label="row.title + ' · ' + row.name"
              :value="row.percent"
              :tone="row.tone"
              size="sm"
            />
          </AppTooltip>
          <span v-else class="modern-home-account-note">{{ t('home.noQuota') }}</span>
        </RouterLink>
        <div
          v-if="row.resetsAt || row.credits || row.observationState"
          class="modern-home-account-foot"
        >
          <AppOverflowText
            v-if="row.resetsAt"
            :text="t('credentialCards.resetsAt', { time: credentialTime(row.resetsAt, locale) })"
          />
          <span v-if="row.observationState">{{
            t('credentialCards.observation.' + row.observationState)
          }}</span>
          <AppTooltip v-if="row.credits" :label="creditHint(row)">
            <AppBadge size="xs" variant="plain" tabindex="0">{{
              t('credentialCards.resetCreditsShort', { count: n(row.credits) })
            }}</AppBadge>
          </AppTooltip>
        </div>
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
  gap: var(--modern-space-1-5);
  min-width: 0;
}
.modern-home-accounts > li + li {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-3);
}
.modern-home-account-link {
  display: grid;
  gap: var(--modern-space-2);
  min-width: 0;
  border-radius: var(--modern-radius-small);
}
.modern-home-account-head {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-account-name {
  font-weight: var(--modern-weight-medium);
}
.modern-home-account-link:hover .modern-home-account-name {
  color: var(--modern-accent);
  text-decoration: underline;
  text-underline-offset: var(--modern-space-1);
}
.modern-home-account-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-account-meta > :not(.modern-home-account-plan) {
  flex: none;
}
.modern-home-account-plan {
  flex: 1;
}
.modern-home-account-percent {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  font-weight: var(--modern-weight-medium);
}
.modern-home-account-percent[data-tone='danger'] {
  color: var(--modern-danger);
}
.modern-home-account-percent[data-tone='warning'] {
  color: var(--modern-warning);
}
.modern-home-account-note,
.modern-home-account-foot {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-home-account-foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1) var(--modern-space-2);
  min-width: 0;
}
</style>
