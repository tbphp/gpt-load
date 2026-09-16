<script setup lang="ts">
import { Boxes, KeyRound, Layers2 } from '@lucide/vue'
import { computed, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import type { HomeBase } from '@modern/api/home'
import { AppBadge, AppIcon } from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'

const props = defineProps<{ base: HomeBase; admin: boolean }>()
const { t, n, locale } = useI18n()
const compact = (value: number) => formatCompactNumber(value, locale.value)
interface Fact {
  key: string
  icon: Component
  value: string
  to?: RouteLocationRaw
}
const facts = computed<Fact[]>(() => [
  {
    key: 'groups',
    icon: Layers2,
    value: compact(props.base.groups),
    ...(props.admin ? { to: { name: 'modern-groups' } } : {}),
  },
  { key: 'models', icon: Boxes, value: compact(props.base.models), to: { name: 'modern-models' } },
  ...(props.admin
    ? [
        {
          key: 'credentials',
          icon: KeyRound,
          value: compact(props.base.available) + ' / ' + compact(props.base.credentials),
          to: { name: 'modern-health' } as RouteLocationRaw,
        },
        {
          key: 'keys',
          icon: KeyRound,
          value: compact(props.base.keys.length),
          to: { name: 'modern-access-keys' } as RouteLocationRaw,
        },
      ]
    : []),
])
/* 页面能打开只说明控制面活着，真正决定网关能不能转发的是有没有可用凭据。 */
const state = computed(() =>
  props.base.credentials > 0 && props.base.available === 0
    ? { key: 'home.stateStalled', tone: 'danger' as const }
    : { key: 'home.stateHealthy', tone: 'success' as const },
)
const uptime = computed(() => {
  const hours = Math.floor(Math.max(0, props.base.observedAt - props.base.startedAt) / 3600000)
  return t('home.uptime', { days: n(Math.floor(hours / 24)), hours: n(hours % 24) })
})
</script>

<template>
  <section class="modern-home-status" :aria-label="t('home.overview')">
    <AppBadge :tone="state.tone" size="xs" dot>{{ t(state.key) }}</AppBadge>
    <span class="modern-home-status-rule" aria-hidden="true" />
    <dl class="modern-home-status-facts">
      <div v-for="fact in facts" :key="fact.key">
        <dt><AppIcon :icon="fact.icon" size="sm" />{{ t('home.' + fact.key) }}</dt>
        <dd>
          <RouterLink v-if="fact.to" :to="fact.to">{{ fact.value }}</RouterLink>
          <span v-else>{{ fact.value }}</span>
        </dd>
      </div>
    </dl>
    <p class="modern-home-status-build">
      <span>{{ base.version }}</span
      >{{ uptime }}
    </p>
  </section>
</template>

<style scoped>
.modern-home-status {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2) var(--modern-space-4);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
  padding: var(--modern-space-3) var(--modern-space-5);
}
.modern-home-status-rule {
  width: var(--modern-line-width);
  height: var(--modern-space-4);
  background: var(--modern-border);
}
.modern-home-status-facts {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: var(--modern-space-2) var(--modern-space-5);
  margin: 0;
}
/* 标签在前、数值在后的一行式事实，不用大字号：库存不是首页的主角。 */
.modern-home-status-facts > div {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-home-status-facts dt {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-status-facts dd {
  margin: 0;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-home-status-facts a {
  color: inherit;
}
.modern-home-status-facts a:hover {
  color: var(--modern-accent);
}
.modern-home-status-build {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  margin-inline-start: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-home-status-build > span {
  font-family: var(--modern-font-mono);
}
</style>
