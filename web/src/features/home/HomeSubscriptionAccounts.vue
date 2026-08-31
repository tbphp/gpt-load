<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { HomeSubscriptionAccountsDto } from '@/app/resources/home'
import SubscriptionAccountCard from '@/features/groups/credentials/SubscriptionAccountCard.vue'

import HomeSectionHeading from './HomeSectionHeading.vue'

defineProps<{ accounts: HomeSubscriptionAccountsDto }>()

const { t } = useI18n()

function ignoreReadonlyProxyMutation(): Promise<void> {
  return Promise.resolve()
}
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
    <div class="home-subscription-accounts__grid">
      <SubscriptionAccountCard
        v-for="account in accounts.items"
        :key="`${account.channel_id}-${account.credential.credential_id}`"
        :item="account.credential"
        :selected="false"
        :busy="false"
        :refreshing-observation="false"
        observation-error=""
        :detail-busy="false"
        :detail-loaded="false"
        detail-error=""
        :channel-icon="account.channel_icon"
        :channel-mark="account.channel_mark"
        :capabilities="account.capabilities"
        :save-proxy="ignoreReadonlyProxyMutation"
        :group-count="account.group_count"
        :available-group-count="account.available_group_count"
        readonly
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

.home-subscription-accounts__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(420px, 1fr));
  align-items: start;
  gap: var(--space-3);
}

@media (max-width: 680px) {
  .home-subscription-accounts__grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
