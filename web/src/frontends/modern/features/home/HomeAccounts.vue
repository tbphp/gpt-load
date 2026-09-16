<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HomeAccount } from '@modern/api/home'
import type { CredentialQuota } from '@modern/api/credential-observation'
import {
  AppBadge,
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

const props = defineProps<{ accounts: HomeAccount[] }>()
const { t, te, n, locale } = useI18n()
/* 首页侧栏只列前几个，完整清单在分组页。 */
const visibleLimit = 5
/* 一个账号只画一条：剩余最少的那个窗口。其余窗口在分组页展开看，
   侧栏 312px 里堆两三条进度条会把下面几块全顶到折叠线以下。 */
function tightest(windows: readonly CredentialQuota[]): CredentialQuota | undefined {
  return windows.reduce<CredentialQuota | undefined>((tight, window) => {
    const percent = quotaRemaining(window)
    if (percent === undefined) return tight
    const current = tight ? quotaRemaining(tight) : undefined
    return current === undefined || percent < current ? window : tight
  }, undefined)
}
function windowTitle(window: CredentialQuota): string {
  const key = 'credentialCards.quotaLabels.' + window.labelKey
  return quotaWindowTitle(window, window.labelKey && te(key) ? t(key) : window.label)
}
const rows = computed(() =>
  props.accounts.slice(0, visibleLimit).map((account) => {
    const observation = account.credential.observation
    const window = observation ? tightest(observation.windows) : undefined
    const status = credentialStatus(account.credential)
    const percent = window ? quotaRemaining(window) : undefined
    const tone = window ? quotaTone(window) : 'neutral'
    return {
      id: account.credential.id,
      channelID: account.channelID,
      name: account.credential.account || account.credential.mask || account.channelName,
      plan: observation?.plan || account.channelName,
      window,
      title: window ? windowTitle(window) : '',
      percent,
      tone,
      // 额度紧张时才写重置时刻：宽裕的时候这行只是噪音。
      resetsAt: window && tone !== 'success' ? window.resetsAt : undefined,
      credits: observation?.resetCredits ?? 0,
      // 可用是常态，不标；只有需要处理的状态才出徽章。
      status: status.tone === 'success' ? undefined : status,
    }
  }),
)
function creditHint(row: (typeof rows.value)[number]): string {
  const account = props.accounts.find((item) => item.credential.id === row.id)
  return (
    account?.credential.observation?.creditExpirations
      .filter((time): time is number => time !== undefined)
      .map((time) => credentialTime(time, locale.value))
      .join('\n') || t('credentialCards.resetCredits', { count: n(row.credits) })
  )
}
</script>

<template>
  <AppPanel :title="t('home.activeAccounts')" :description="t('home.recentAccounts')" compact>
    <template #actions
      ><AppButton as-child size="xs" variant="text"
        ><RouterLink :to="{ name: 'modern-groups' }">{{ t('home.viewAll') }}</RouterLink></AppButton
      ></template
    >
    <ul class="modern-home-accounts">
      <li v-for="row in rows" :key="row.id">
        <RouterLink :to="{ name: 'modern-groups', query: { channel: row.channelID } }">
          <span class="modern-home-account-head">
            <AppOverflowText class="modern-home-account-name" :text="row.name" />
            <span class="modern-home-account-plan">{{ row.plan }}</span>
            <AppBadge v-if="row.status" :tone="row.status.tone" size="xs" variant="plain" dot>{{
              t(row.status.key)
            }}</AppBadge>
            <span
              v-if="row.percent !== undefined"
              class="modern-home-account-percent"
              :data-tone="row.tone"
              >{{
                t('credentialCards.remaining', { value: n(Math.round(row.percent)) + '%' })
              }}</span
            >
          </span>
          <AppProgressBar
            v-if="row.window"
            :label="`${row.title} · ${row.name}`"
            :value="row.percent"
            :tone="row.tone"
            size="sm"
          />
          <span v-else class="modern-home-account-note">{{ t('home.noQuota') }}</span>
          <span v-if="row.resetsAt" class="modern-home-account-note">{{
            t('credentialCards.resetsAt', { time: credentialTime(row.resetsAt, locale) })
          }}</span>
        </RouterLink>
        <AppTooltip v-if="row.credits" :label="creditHint(row)"
          ><AppBadge size="xs" tabindex="0">{{
            t('credentialCards.resetCreditsShort', { count: n(row.credits) })
          }}</AppBadge></AppTooltip
        >
      </li>
    </ul>
  </AppPanel>
</template>

<style scoped>
.modern-home-accounts {
  display: grid;
  gap: var(--modern-space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-home-accounts > li {
  display: grid;
  gap: var(--modern-space-1-5);
  min-width: 0;
}
.modern-home-accounts a {
  display: grid;
  gap: var(--modern-space-1-5);
  min-width: 0;
}
.modern-home-account-head {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-2);
  min-width: 0;
  font-size: var(--modern-font-size-secondary);
}
/* 名字长就截断名字，套餐名先被挤掉；剩余百分比永远不收缩，它是这行的结论。 */
.modern-home-account-name {
  min-width: 0;
  flex: 0 1 auto;
  font-weight: var(--modern-weight-medium);
}
.modern-home-account-plan {
  overflow: hidden;
  min-width: 0;
  flex: 1 1 auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-home-account-head :deep(.modern-badge) {
  flex: none;
  white-space: nowrap;
}
.modern-home-account-percent {
  flex: none;
  margin-inline-start: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-home-account-percent[data-tone='danger'] {
  color: var(--modern-danger);
}
.modern-home-account-percent[data-tone='warning'] {
  color: var(--modern-warning);
}
.modern-home-account-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
</style>
