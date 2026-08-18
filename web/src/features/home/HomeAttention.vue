<script setup lang="ts">
import { CircleAlert, TriangleAlert } from '@lucide/vue'
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
      class="home-attention__row home-attention__row--danger"
      :to="monitorLocation({ tab: 'health' })"
    >
      <CircleAlert :size="14" aria-hidden="true" />
      <span>{{ t('home.ledger.attention.summary', { count: total }) }}</span>
      <span class="home-attention__go" aria-hidden="true">
        {{ t('home.ledger.attention.action') }} →
      </span>
    </RouterLink>

    <template v-else>
      <RouterLink
        v-for="item in visibleItems"
        :key="`${item.kind}-${item.groupID}`"
        class="home-attention__row"
        :class="`home-attention__row--${item.kind === 'blacklisted' ? 'danger' : 'warning'}`"
        :to="itemLocation(item.groupID, item.kind)"
      >
        <CircleAlert v-if="item.kind === 'blacklisted'" :size="14" aria-hidden="true" />
        <TriangleAlert v-else :size="14" aria-hidden="true" />
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
        <span class="home-attention__go" aria-hidden="true">
          {{ t('home.ledger.attention.action') }} →
        </span>
      </RouterLink>
    </template>
  </section>
</template>

<style scoped>
.home-attention {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-top: 14px;
}

/*
 * 告警要一眼认出来：按 tone 上底色与描边，配警示图标，回到仓库
 * 「图标 + 文字 + 颜色」的三重编码。原来只有一个 6px 圆点承载严重度。
 */
.home-attention__row {
  display: flex;
  align-items: center;
  gap: 9px;
  border: 1px solid transparent;
  border-radius: var(--radius-control);
  padding: 8px 11px;
  font-size: var(--text-meta);
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.home-attention__row svg {
  flex: none;
}

.home-attention__row--warning {
  border-color: color-mix(in srgb, var(--color-warning) 30%, var(--color-border-subtle));
  background: var(--color-warning-bg);
  color: var(--color-warning);
}

.home-attention__row--danger {
  border-color: color-mix(in srgb, var(--color-danger) 30%, var(--color-border-subtle));
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.home-attention__row--warning:hover {
  border-color: var(--color-warning);
}

.home-attention__row--danger:hover {
  border-color: var(--color-danger);
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
  font-weight: 650;
  white-space: nowrap;
}
</style>
