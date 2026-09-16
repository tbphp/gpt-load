<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { AccessKeyRow, CostWindow } from '@modern/api/access-keys'
import { AppBadge, AppButton, AppPanel, AppProgressBar } from '@modern/components/ui'
import {
  accessNanoUSD,
  accessState,
  accessTime,
} from '@modern/features/access-keys/access-key-display'

const props = defineProps<{ rows: AccessKeyRow[]; selected: number }>()
defineEmits<{ select: [number] }>()
const { t, n, locale } = useI18n()
/* 一个密钥可能配了多条额度规则，这里只挑最紧的那条：面板里放不下也不需要放下全部。 */
function tightest(row: AccessKeyRow): CostWindow | undefined {
  return row.cost_limit_status?.rules.reduce<CostWindow | undefined>((tight, window) => {
    const limit = accessNanoUSD(window.limit_usd)
    if (limit <= 0n) return tight
    const share = Number((accessNanoUSD(window.remaining_usd) * 10000n) / limit)
    if (!tight) return window
    const current = accessNanoUSD(tight.limit_usd)
    return share < Number((accessNanoUSD(tight.remaining_usd) * 10000n) / current) ? window : tight
  }, undefined)
}
const items = computed(() =>
  props.rows.map((row) => {
    const window = tightest(row)
    const limit = window ? accessNanoUSD(window.limit_usd) : 0n
    return {
      row,
      status: accessState(row),
      percent: window
        ? Math.max(
            0,
            Math.min(100, Number((accessNanoUSD(window.remaining_usd) * 10000n) / limit) / 100),
          )
        : undefined,
      exhausted: window?.status === 'exhausted',
    }
  }),
)
</script>

<template>
  <AppPanel :title="t('home.keys')" compact>
    <template #actions
      ><AppButton as-child size="xs" variant="text"
        ><RouterLink :to="{ name: 'modern-access-keys' }">{{
          t('home.viewAll')
        }}</RouterLink></AppButton
      ></template
    >
    <ul class="modern-home-keys">
      <li v-for="item in items" :key="item.row.id">
        <AppButton
          variant="ghost"
          :aria-pressed="item.row.id === selected"
          @click="$emit('select', item.row.id)"
        >
          <span class="modern-home-key-head">
            <span class="modern-home-key-name">{{ item.row.name }}</span>
            <AppBadge :tone="item.status.tone" size="xs" variant="plain" dot>{{
              t('accessKeys.' + item.status.key)
            }}</AppBadge>
          </span>
          <AppProgressBar
            v-if="item.percent !== undefined"
            :label="t('home.quota')"
            :value="item.percent"
            :tone="item.exhausted ? 'danger' : 'info'"
            size="sm"
          />
          <!-- 额度与上次请求合成一行：两条都是次要信息，各占一行会把面板拉长一截。 -->
          <span class="modern-home-key-meta">
            <span v-if="item.percent !== undefined">{{
              t('home.quotaLeft', { percent: n(Math.round(item.percent)) })
            }}</span>
            <span>{{
              t('home.lastRequestAt', { time: accessTime(item.row.last_request_at_ms, locale) })
            }}</span>
          </span>
        </AppButton>
      </li>
    </ul>
  </AppPanel>
</template>

<style scoped>
.modern-home-keys {
  display: grid;
  gap: var(--modern-space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}
/* 整块可点：点一个密钥就把左边的接入配置切过去，两边说的是同一件事。 */
.modern-home-keys :deep(.modern-button) {
  width: 100%;
  height: auto;
  flex-direction: column;
  align-items: stretch;
  gap: var(--modern-space-1-5);
  padding: var(--modern-space-2) var(--modern-space-2);
  font-weight: var(--modern-weight-regular);
  text-align: start;
}
.modern-home-keys :deep(.modern-button[aria-pressed='true']) {
  background: var(--modern-accent-soft);
}
.modern-home-key-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-home-key-name {
  overflow: hidden;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-home-key-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-key-meta {
  font-variant-numeric: tabular-nums;
}
.modern-home-key-meta > span + span::before {
  margin-inline-end: var(--modern-space-1);
  content: '·';
}
</style>
