<script setup lang="ts">
import { ArrowRight, Check, KeyRound } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { AccessKeyRow, CostWindow } from '@modern/api/access-keys'
import {
  AppBadge,
  AppButton,
  AppIcon,
  AppOverflowText,
  AppPanel,
  AppProgressBar,
} from '@modern/components/ui'
import { accessNanoUSD, accessState } from '@modern/features/access-keys/access-key-display'
import { credentialTime } from '@modern/features/groups/credential-presentation'

const props = defineProps<{
  rows: AccessKeyRow[]
  selected: number
  failed: boolean
  loading: boolean
}>()
defineEmits<{ select: [number]; retry: [] }>()
const { t, n, locale } = useI18n()
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
      status: status.key === 'active' ? undefined : status,
    }
  }),
)
</script>

<template>
  <AppPanel :title="t('home.keys')" :description="t('home.recentKeys')" compact>
    <template #actions>
      <AppButton as-child size="xs" variant="text">
        <RouterLink :to="{ name: 'modern-access-keys' }">
          {{ t('home.viewAll') }}<AppIcon :icon="ArrowRight" size="sm" />
        </RouterLink>
      </AppButton>
    </template>
    <div v-if="failed" class="modern-home-key-state" role="status">
      <span>{{ t(rows.length ? 'home.refreshFailed' : 'home.keysFailed') }}</span>
      <AppButton size="xs" variant="text" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-if="!rows.length && !failed" class="modern-home-key-state">
      {{ t(loading ? 'ui.loading' : 'home.noKeys') }}
    </p>
    <ul v-if="rows.length" class="modern-home-keys">
      <li v-for="item in items" :key="item.row.id">
        <AppButton
          variant="ghost"
          class="modern-home-key-entry"
          :aria-pressed="item.row.id === selected"
          @click="$emit('select', item.row.id)"
        >
          <span class="modern-home-key-head">
            <AppIcon :icon="KeyRound" size="sm" class="modern-home-key-icon" />
            <AppOverflowText class="modern-home-key-name" :text="item.row.name" />
            <AppBadge v-if="item.status" :tone="item.status.tone" size="xs" variant="plain" dot>
              {{ t('accessKeys.' + item.status.key) }}
            </AppBadge>
            <AppIcon
              v-else-if="item.row.id === selected"
              :icon="Check"
              size="sm"
              class="modern-home-key-selected"
            />
          </span>
          <span class="modern-home-key-meta">
            <AppOverflowText :text="item.row.masked_key" class="modern-home-key-mask" />
            <span
              v-if="item.percent !== undefined"
              class="modern-home-key-quota-fact"
              :class="{ 'is-exhausted': item.exhausted }"
            >
              {{ t('home.quotaLeft', { percent: n(Math.round(item.percent)) }) }}
            </span>
          </span>
          <AppProgressBar
            v-if="item.percent !== undefined"
            :label="t('home.quota') + ' · ' + item.row.name"
            :value="item.percent"
            :tone="item.exhausted ? 'danger' : 'info'"
            size="sm"
          />
          <AppOverflowText
            v-else
            class="modern-home-key-last-request"
            :text="
              item.row.last_request_at_ms
                ? t('home.lastRequestAt', {
                    time: credentialTime(item.row.last_request_at_ms, locale),
                  })
                : t('home.neverRequested')
            "
          />
        </AppButton>
      </li>
    </ul>
  </AppPanel>
</template>

<style scoped>
.modern-home-key-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
}
.modern-home-keys {
  display: grid;
  gap: var(--modern-space-1);
  min-width: 0;
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-home-key-state + .modern-home-keys {
  margin-top: var(--modern-space-3);
}
.modern-home-keys > li {
  min-width: 0;
}
.modern-home-key-entry {
  display: grid;
  width: 100%;
  min-width: 0;
  justify-content: stretch;
  gap: var(--modern-space-1-5);
  padding: var(--modern-space-2);
  color: var(--modern-text);
  font-weight: var(--modern-weight-regular);
  text-align: start;
}
.modern-home-key-entry[aria-pressed='true'] {
  background: var(--modern-accent-soft);
}
.modern-home-key-head {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-home-key-name {
  flex: 1;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-home-key-icon {
  color: var(--modern-muted);
}
.modern-home-key-selected {
  color: var(--modern-accent);
}
.modern-home-key-meta {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--modern-space-2);
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-home-key-quota-fact {
  flex: none;
}
.modern-home-key-mask {
  font-family: var(--modern-font-mono);
}
.modern-home-key-last-request {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-home-key-meta .is-exhausted {
  color: var(--modern-danger);
}
</style>
