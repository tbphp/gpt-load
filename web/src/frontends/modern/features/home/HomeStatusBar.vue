<script setup lang="ts">
import { ArrowRight, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import type { HomeBase } from '@modern/api/home'
import { AppBadge, AppIcon, AppButton } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'

const props = defineProps<{ base: HomeBase; admin: boolean; attention: number }>()
const { t, n, locale } = useI18n()
const compact = (value: number) => formatCompactNumber(value, locale.value)
interface Fact {
  key: string
  value: string
  to?: RouteLocationRaw
}
const facts = computed<Fact[]>(() => [
  {
    key: 'groups',
    value: compact(props.base.groups),
    ...(props.admin ? { to: { name: 'modern-groups' } } : {}),
  },
  { key: 'models', value: compact(props.base.models), to: { name: 'modern-models' } },
  ...(props.admin
    ? [
        {
          key: 'credentials',
          value: compact(props.base.available) + ' / ' + compact(props.base.credentials),
          to: { name: 'modern-health' } as RouteLocationRaw,
        },
        {
          key: 'keys',
          value: compact(props.base.keys.length),
          to: { name: 'modern-access-keys' } as RouteLocationRaw,
        },
      ]
    : []),
])
const state = computed(() => {
  if (props.base.credentials === 0) return { key: 'home.statePending', tone: 'neutral' as const }
  if (props.base.available === 0) return { key: 'home.stateStalled', tone: 'danger' as const }
  return { key: 'home.stateHealthy', tone: 'success' as const }
})
const uptime = computed(() => {
  const minutes = Math.floor(Math.max(0, props.base.observedAt - props.base.startedAt) / 60000)
  if (minutes < 60) return t('home.uptimeMinutes', { count: n(minutes) })
  const hours = Math.floor(minutes / 60)
  return t('home.uptime', { days: n(Math.floor(hours / 24)), hours: n(hours % 24) })
})
</script>

<template>
  <section class="modern-home-status" :aria-label="t('home.overview')">
    <div class="modern-home-status-summary">
      <AppBadge :tone="state.tone" dot>{{ t(state.key) }}</AppBadge>
      <dl class="modern-home-status-facts">
        <div v-for="fact in facts" :key="fact.key">
          <dt>{{ t('home.' + fact.key) }}</dt>
          <dd>
            <RouterLink v-if="fact.to" :to="fact.to">{{ fact.value }}</RouterLink>
            <span v-else>{{ fact.value }}</span>
          </dd>
        </div>
      </dl>
      <AppButton
        v-if="attention"
        as-child
        size="xs"
        variant="ghost"
        class="modern-home-status-attention"
      >
        <RouterLink :to="{ name: 'modern-health' }">
          <AppIcon :icon="TriangleAlert" size="sm" />
          {{ t('home.attention.count', { count: n(attention) }) }}
          <AppIcon :icon="ArrowRight" size="xs" />
        </RouterLink>
      </AppButton>
    </div>
    <p class="modern-home-status-build">
      <span>{{ base.version }}</span>
      <span>{{ uptime }}</span>
    </p>
  </section>
</template>

<style scoped>
.modern-home-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3) var(--modern-space-5);
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-4) var(--modern-space-5);
}
.modern-home-status-summary {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3) var(--modern-space-5);
  min-width: 0;
}
.modern-home-status-facts {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3) var(--modern-space-5);
  margin: 0;
  border-inline-start: var(--modern-line-width) solid var(--modern-border);
  padding-inline-start: var(--modern-space-5);
}
.modern-home-status-facts > div {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-1-5);
  white-space: nowrap;
}
.modern-home-status-facts dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-status-facts dd {
  order: -1;
  margin: 0;
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-home-status-facts a {
  color: inherit;
}
.modern-home-status-facts a:hover {
  color: var(--modern-accent);
  text-decoration: underline;
  text-underline-offset: var(--modern-space-1);
}
.modern-home-status-attention {
  background: var(--modern-warning-soft);
  color: var(--modern-warning);
}
.modern-home-status-build {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2) var(--modern-space-3);
  margin-inline-start: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-status-build span {
  white-space: nowrap;
}
.modern-home-status-build > span:first-child {
  font-family: var(--modern-font-mono);
}
@media (max-width: 760px) {
  .modern-home-status-facts {
    width: 100%;
    border-inline-start: 0;
    padding-inline-start: 0;
  }
}
</style>
