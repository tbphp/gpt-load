<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HomeAccount } from '@modern/api/home'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppOverflowText,
  AppPanel,
  AppTooltip,
} from '@modern/components/ui'
import { credentialStatus, credentialTime } from '@modern/features/groups/credential-presentation'
import CredentialQuotaRows from '@modern/features/groups/CredentialQuotaRows.vue'

const props = defineProps<{ accounts: HomeAccount[] }>()
const { t, n, locale } = useI18n()
/* 首页侧栏只放前几个，完整清单在分组页；账号多的时候这块会把下面三块全顶到折叠线以下。 */
const visibleLimit = 3
const rows = computed(() =>
  props.accounts.slice(0, visibleLimit).map((account) => {
    const status = credentialStatus(account.credential)
    const exhausted = account.credential.observation?.windows.some(
      (window) => window.scope === 'account' && window.state === 'exhausted',
    )
    return {
      ...account,
      status:
        status.key === 'groups.credentials.available' && exhausted
          ? { key: 'accessKeys.exhausted', tone: 'warning' as const }
          : status,
    }
  }),
)
function creditHint(row: HomeAccount): string {
  return (
    row.credential.observation?.creditExpirations
      .filter((time): time is number => time !== undefined)
      .map((time) => credentialTime(time, locale.value))
      .join('\n') ||
    t('credentialCards.resetCredits', { count: n(row.credential.observation?.resetCredits ?? 0) })
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
    <div class="modern-home-accounts">
      <article v-for="row in rows" :key="row.credential.id" class="modern-home-account">
        <header>
          <AppChannelIcon
            :icon="row.channelIcon"
            :mark="row.channelMark"
            :name="row.channelName"
            size="md"
            :tooltip="false"
          />
          <div>
            <AppOverflowText
              :text="row.credential.account || row.credential.mask || row.channelName"
            /><span>{{ row.credential.observation?.plan || row.channelName }}</span>
          </div>
          <AppBadge :tone="row.status.tone" size="xs" variant="plain" dot>{{
            t(row.status.key)
          }}</AppBadge>
        </header>
        <CredentialQuotaRows
          v-if="row.credential.observation?.windows.length"
          :windows="row.credential.observation.windows"
        />
        <p v-else class="modern-home-account-note">{{ t('home.noQuota') }}</p>
        <p
          v-if="row.credential.observation && row.credential.observation.state !== 'fresh'"
          class="modern-home-account-note"
        >
          {{ t('credentialCards.observation.' + row.credential.observation.state) }}
        </p>
        <!-- 重置券并进页脚，并且不再重复「更新于」：页头已经有整页的刷新时间。 -->
        <footer>
          <span>{{
            t('home.groupCount', { available: n(row.availableGroups), total: n(row.groups) })
          }}</span>
          <AppTooltip v-if="row.credential.observation?.resetCredits" :label="creditHint(row)"
            ><AppBadge size="xs" tabindex="0">{{
              t('credentialCards.resetCreditsShort', {
                count: n(row.credential.observation.resetCredits),
              })
            }}</AppBadge></AppTooltip
          >
          <AppButton as-child variant="text" size="xs"
            ><RouterLink :to="{ name: 'modern-groups', query: { channel: row.channelID } }">{{
              t('inspector.openGroup')
            }}</RouterLink></AppButton
          >
        </footer>
      </article>
    </div>
  </AppPanel>
</template>

<style scoped>
.modern-home-accounts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 280px), 1fr));
  gap: var(--modern-space-3);
}
.modern-home-account {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-3);
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3);
}
.modern-home-account header {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-home-account header > div {
  display: grid;
  min-width: 0;
  flex: 1;
  gap: var(--modern-space-1);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-home-account header > div > span:last-child {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
}
.modern-home-account footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  margin-top: auto;
  gap: var(--modern-space-2);
}
.modern-home-account footer > :last-child {
  margin-inline-start: auto;
}
.modern-home-account footer,
.modern-home-account-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
</style>
