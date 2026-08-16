<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { RuntimeHealthDto } from '@/app/resources/health'
import { groupDetailLocation, monitorLocation } from '@/app/route-locations'
import AppRelativeTime from '@/components/ui/AppRelativeTime.vue'

import { attentionRowLimit, attentionTotal, collectAttentionItems } from './attention'

const props = defineProps<{ health: RuntimeHealthDto | null }>()

const { locale, t } = useI18n()

const items = computed(() =>
  props.health === null
    ? []
    : collectAttentionItems(
        props.health.blacklisted_credentials,
        props.health.low_quota_credentials,
      ),
)
const overflowing = computed(() => items.value.length > attentionRowLimit)
const visibleItems = computed(() => items.value.slice(0, attentionRowLimit))
const total = computed(() => attentionTotal(items.value))

function itemLocation(groupID: number, kind: string) {
  return groupDetailLocation(
    groupID,
    kind === 'blacklisted'
      ? { tab: 'credentials', credential_status: 'blacklisted' }
      : { tab: 'credentials' },
  )
}

function quotaPercent(remaining: number): string {
  return new Intl.NumberFormat(locale.value, {
    style: 'percent',
    maximumFractionDigits: 0,
  }).format(remaining)
}
</script>

<template>
  <!-- 条件区：没有要处理的事就整块不渲染——不留标题、不留边框、不留「一切正常」的绿条。 -->
  <section v-if="items.length > 0" class="home-attention" aria-labelledby="home-attention-title">
    <h2 id="home-attention-title" class="sr-only">{{ t('home.ledger.attention.title') }}</h2>

    <RouterLink
      v-if="overflowing"
      class="home-attention__row"
      :to="monitorLocation({ tab: 'health' })"
    >
      <span class="home-attention__dot" data-tone="danger" aria-hidden="true"></span>
      <span>{{ t('home.ledger.attention.summary', { count: total }) }}</span>
      <span class="home-attention__go" aria-hidden="true">→</span>
    </RouterLink>

    <template v-else>
      <RouterLink
        v-for="item in visibleItems"
        :key="`${item.kind}-${item.groupID}`"
        class="home-attention__row"
        :to="itemLocation(item.groupID, item.kind)"
      >
        <span
          class="home-attention__dot"
          :data-tone="item.kind === 'blacklisted' ? 'danger' : 'warning'"
          aria-hidden="true"
        ></span>
        <span v-if="item.kind === 'blacklisted'">
          {{ t('home.ledger.attention.blacklisted', { group: item.groupName, count: item.value }) }}
        </span>
        <span v-else class="home-attention__quota">
          {{
            t('home.ledger.attention.lowQuota', {
              group: item.groupName,
              remaining: quotaPercent(item.value),
            })
          }}
          <AppRelativeTime
            v-if="item.resetAtMS !== undefined"
            :instant="item.resetAtMS"
            :locale="locale"
            :empty-label="''"
          />
        </span>
        <span class="home-attention__go" aria-hidden="true">→</span>
      </RouterLink>
    </template>
  </section>
</template>

<style scoped>
.home-attention {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-top: 14px;
}

.home-attention__row {
  display: flex;
  align-items: center;
  gap: 9px;
  margin-inline: -10px;
  border-radius: var(--radius-control);
  padding: 8px 10px;
  color: var(--color-text);
  font-size: var(--text-meta);
  transition: background-color var(--duration-fast) var(--easing-standard);
}

.home-attention__row:hover {
  background: var(--color-interactive-hover);
}

.home-attention__dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--color-danger);
}

.home-attention__dot[data-tone='warning'] {
  background: var(--color-warning);
}

.home-attention__quota {
  display: inline-flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 5px;
  min-width: 0;
}

.home-attention__go {
  margin-left: auto;
  color: var(--color-action);
  font-family: var(--font-mono);
  font-weight: 600;
}
</style>
