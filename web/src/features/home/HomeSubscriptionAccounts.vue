<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { HomeSubscriptionAccountsDto } from '@/app/resources/home'

import HomeSubscriptionAccountMiniCard from './HomeSubscriptionAccountMiniCard.vue'
import HomeSectionHeading from './HomeSectionHeading.vue'

defineProps<{ accounts: HomeSubscriptionAccountsDto }>()

const { t } = useI18n()
</script>

<template>
  <section
    v-if="accounts.items.length > 0"
    class="home-subscription-accounts"
    aria-labelledby="home-subscription-accounts-title"
  >
    <HomeSectionHeading
      id="home-subscription-accounts-title"
      :title="t('home.ledger.subscriptionAccounts.title')"
    />
    <div class="home-subscription-accounts__row">
      <HomeSubscriptionAccountMiniCard
        v-for="account in accounts.items"
        :key="`${account.channel_id}-${account.credential.credential_id}`"
        :account="account"
      />
    </div>
  </section>
</template>

<style scoped>
.home-subscription-accounts {
  display: grid;
  gap: var(--space-3);
  margin-top: 36px;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 20px;
}

/*
 * 桌面端只占一行：卡片给定基准宽度而非等分整行宽度，够 4 张时都拉伸到舒适宽度，
 * 超过时横向滚动，绝不换成第二行。窄视口下基准宽度撑不满屏，天然退化为可滚动的
 * 横向列表，无需为移动端单独处理。
 */
.home-subscription-accounts__row {
  display: flex;
  gap: 10px;
  overflow-x: auto;
  overscroll-behavior-inline: contain;
  padding-bottom: 2px;
  scroll-snap-type: x proximity;
}

.home-subscription-accounts__row > * {
  min-width: 208px;
  flex: 1 1 232px;
  scroll-snap-align: start;
}
</style>
