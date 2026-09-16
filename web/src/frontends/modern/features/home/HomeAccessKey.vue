<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccessKeyRow, CostRule, CostWindow } from '@modern/api/access-keys'
import {
  AppBadge,
  AppOverflowText,
  AppPanel,
  AppProgressBar,
  AppProtocolTag,
  AppTag,
} from '@modern/components/ui'
import {
  accessNanoUSD,
  accessState,
  accessTime,
} from '@modern/features/access-keys/access-key-display'
import { formatNanoUSD } from '@modern/components/ui/format'

const props = defineProps<{ row: AccessKeyRow }>()
const { t, n, locale } = useI18n()
const status = computed(() => accessState(props.row))
const windows = computed(
  () => new Map(props.row.cost_limit_status?.rules.map((window) => [window.id, window])),
)
const money = (value: string) =>
  formatNanoUSD(String(accessNanoUSD(value)), locale.value, 'narrowSymbol', 2)
function title(rule: CostRule): string {
  if (rule.kind === 'total') return t('home.totalQuota')
  const seconds = rule.period_seconds ?? 0
  const days = seconds % 86400 === 0
  return t('home.periodQuota', {
    count: n(seconds / (days ? 86400 : 3600)),
    unit: t(days ? 'home.days' : 'home.hours'),
  })
}
function percent(window?: CostWindow): number | undefined {
  if (!window) return undefined
  const limit = accessNanoUSD(window.limit_usd)
  return limit > 0n
    ? Math.max(
        0,
        Math.min(100, Number((accessNanoUSD(window.remaining_usd) * 10000n) / limit) / 100),
      )
    : 0
}
</script>

<template>
  <AppPanel :title="row.name" compact>
    <template #actions
      ><AppBadge :tone="status.tone" size="xs" dot>{{
        t('accessKeys.' + status.key)
      }}</AppBadge></template
    >
    <div class="modern-home-key">
      <AppOverflowText class="modern-home-key-mask" :text="row.masked_key" />
      <dl class="modern-home-key-facts">
        <div>
          <dt>{{ t('home.expires') }}</dt>
          <dd>{{ row.expires_at_ms ? accessTime(row.expires_at_ms, locale) : t('home.never') }}</dd>
        </div>
        <div>
          <dt>{{ t('home.rpm') }}</dt>
          <dd>{{ row.rpm_limit ? n(row.rpm_limit) : t('home.unlimited') }}</dd>
        </div>
        <div>
          <dt>{{ t('home.multiplier') }}</dt>
          <dd>×{{ row.price_multiplier }}</dd>
        </div>
        <div>
          <dt>{{ t('home.lastRequest') }}</dt>
          <dd>{{ accessTime(row.last_request_at_ms, locale) }}</dd>
        </div>
      </dl>
      <div class="modern-home-key-scope">
        <span>{{ t('home.protocols') }}</span>
        <div>
          <AppProtocolTag
            v-for="protocol in row.filters.protocols"
            :key="protocol"
            :protocol="protocol"
          /><span v-if="!row.filters.protocols.length">{{ t('home.allProtocols') }}</span>
        </div>
        <span>{{ t('home.modelsScope') }}</span>
        <div>
          <AppTag
            v-for="model in row.filters.models"
            :key="model"
            :text="model"
            size="xs"
            tone="neutral"
          /><span v-if="!row.filters.models.length">{{ t('home.allModels') }}</span>
        </div>
        <template v-if="row.filters.allowed_cidrs.length"
          ><span>{{ t('home.sources') }}</span>
          <div>
            <AppTag
              v-for="source in row.filters.allowed_cidrs"
              :key="source"
              :text="source"
              size="xs"
              tone="neutral"
            /></div
        ></template>
      </div>
      <span v-if="row.filters.groups.length" class="modern-home-key-note">{{
        t('home.limitedGroups', { count: n(row.filters.groups.length) })
      }}</span>
      <div v-if="row.cost_limit_rules.length" class="modern-home-key-quotas">
        <div v-for="rule in row.cost_limit_rules" :key="rule.id" class="modern-home-key-quota">
          <div>
            <strong>{{ title(rule) }}</strong
            ><span>{{
              windows.get(rule.id!)
                ? t('home.remaining', {
                    remaining: money(windows.get(rule.id!)!.remaining_usd),
                    limit: money(rule.limit_usd),
                  })
                : money(rule.limit_usd)
            }}</span>
          </div>
          <AppProgressBar
            :label="title(rule)"
            :value="percent(windows.get(rule.id!))"
            :tone="windows.get(rule.id!)?.status === 'exhausted' ? 'danger' : 'info'"
            size="sm"
          />
          <div class="modern-home-key-note">
            <span>{{
              windows.get(rule.id!)
                ? t('home.used', { value: money(windows.get(rule.id!)!.used_usd) })
                : t('home.quotaUnavailable')
            }}</span>
            <span v-if="windows.get(rule.id!)?.status === 'inactive'">{{
              t('home.inactive')
            }}</span>
            <span v-else-if="windows.get(rule.id!)?.window_ends_at_ms">{{
              t('home.recovery', {
                time: accessTime(windows.get(rule.id!)!.window_ends_at_ms, locale),
              })
            }}</span>
          </div>
        </div>
      </div>
    </div>
  </AppPanel>
</template>

<style scoped>
.modern-home-key {
  display: grid;
  gap: var(--modern-space-4);
}
.modern-home-key-mask,
.modern-home-key-note {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-key-mask {
  font-family: var(--modern-font-mono);
}
.modern-home-key-facts {
  display: grid;
  gap: var(--modern-space-3);
  margin: 0;
  font-size: var(--modern-font-size-secondary);
}
.modern-home-key-facts > div {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: baseline;
  gap: var(--modern-space-3);
}
.modern-home-key-facts dt {
  color: var(--modern-muted);
}
.modern-home-key-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.modern-home-key-scope {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: start;
  gap: var(--modern-space-3);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-key-scope > span {
  color: var(--modern-muted);
}
.modern-home-key-scope > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-home-key-quotas {
  display: grid;
  gap: var(--modern-space-4);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-4);
}
.modern-home-key-quota {
  display: grid;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-key-quota > div:not([role='progressbar']) {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--modern-space-2);
}
.modern-home-key-quota strong {
  font-weight: var(--modern-weight-medium);
}
</style>
