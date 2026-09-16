<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { AccessKeyRow, CostWindow } from '@modern/api/access-keys'
import {
  AppBadge,
  AppButton,
  AppOverflowText,
  AppPanel,
  AppProgressBar,
} from '@modern/components/ui'
import { accessNanoUSD, accessState } from '@modern/features/access-keys/access-key-display'
import { credentialTime } from '@modern/features/groups/credential-presentation'

const props = defineProps<{ rows: AccessKeyRow[]; selected: number }>()
defineEmits<{ select: [number] }>()
const { t, n, locale } = useI18n()
/* 一个密钥可能配了多条额度规则，只画剩余最少的那条。 */
function share(window: CostWindow): number | undefined {
  const limit = accessNanoUSD(window.limit_usd)
  if (limit <= 0n) return undefined
  return Number((accessNanoUSD(window.remaining_usd) * 10000n) / limit) / 100
}
const items = computed(() =>
  props.rows.map((row) => {
    const tightest = row.cost_limit_status?.rules.reduce<
      { window: CostWindow; percent: number } | undefined
    >((tight, window) => {
      const percent = share(window)
      if (percent === undefined) return tight
      return !tight || percent < tight.percent ? { window, percent } : tight
    }, undefined)
    const status = accessState(row)
    return {
      row,
      percent: tightest ? Math.max(0, Math.min(100, tightest.percent)) : undefined,
      exhausted: tightest?.window.status === 'exhausted',
      // 已启用是常态，不标；只有停用、过期、额度用尽才出徽章。
      status: status.key === 'active' ? undefined : status,
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
            <AppOverflowText class="modern-home-key-name" :text="item.row.name" />
            <AppBadge v-if="item.status" :tone="item.status.tone" size="xs" variant="plain" dot>{{
              t('accessKeys.' + item.status.key)
            }}</AppBadge>
            <span class="modern-home-key-fact">{{
              item.percent !== undefined
                ? t('home.quotaLeft', { percent: n(Math.round(item.percent)) })
                : t('home.lastRequestAt', {
                    time: credentialTime(item.row.last_request_at_ms, locale),
                  })
            }}</span>
          </span>
          <AppProgressBar
            v-if="item.percent !== undefined"
            :label="t('home.quota') + ' · ' + item.row.name"
            :value="item.percent"
            :tone="item.exhausted ? 'danger' : 'info'"
            size="sm"
          />
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
/* grid 与 flex 子项默认 min-width:auto，长密钥名会把徽章顶出卡片；
   这条链上每一层都要显式解开才收得住。 */
.modern-home-keys > li {
  min-width: 0;
}
.modern-home-keys :deep(.modern-button) {
  width: 100%;
  min-width: 0;
  height: auto;
  flex-direction: column;
  align-items: stretch;
  gap: var(--modern-space-1-5);
  padding: var(--modern-space-2);
  font-weight: var(--modern-weight-regular);
  text-align: start;
}
.modern-home-keys :deep(.modern-button[aria-pressed='true']) {
  background: var(--modern-accent-soft);
}
.modern-home-key-head {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-home-key-name {
  min-width: 0;
  flex: 0 1 auto;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
/* 徽章是结论，不参与压缩；被挤窄就会折行把整行撑高。 */
.modern-home-key-head :deep(.modern-badge) {
  flex: none;
  white-space: nowrap;
}
.modern-home-key-fact {
  flex: none;
  margin-inline-start: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
</style>
