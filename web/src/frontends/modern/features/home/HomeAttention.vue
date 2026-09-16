<script setup lang="ts">
import { ChevronRight, CircleCheck, CircleSlash, TriangleAlert } from '@lucide/vue'
import { computed, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { GroupRow } from '@modern/api/groups'
import { AppBadge, AppIcon, AppPanel } from '@modern/components/ui'

const props = defineProps<{ groups: GroupRow[] }>()
const { t, n } = useI18n()
/* 首页最多列这么多条，其余折成一行汇总，免得把接入面板顶下去。 */
const visibleLimit = 4
interface AttentionItem {
  key: string
  id: number
  icon: Component
  tone: 'danger' | 'warning'
  subject: string
  detail: string
}
/*
 * 只收「不处理就一直坏着」的两类：分组彻底没有可用凭据、以及个别凭据被拉黑。
 *
 * 刻意不收冷却：几分钟内自愈，看了也不用做任何事。
 * 也刻意不收额度：订阅账号那块已经按账号画了额度条，这里再说一遍就是同一件事两个说法。
 */
const items = computed<AttentionItem[]>(() => {
  const stalled: AttentionItem[] = []
  const degraded: AttentionItem[] = []
  for (const group of props.groups) {
    if (!group.enabled || group.credentials.total === 0) continue
    if (group.credentials.available === 0)
      stalled.push({
        key: 'stalled-' + group.id,
        id: group.id,
        icon: CircleSlash,
        tone: 'danger',
        subject: group.name,
        detail: t('home.attention.stalled'),
      })
    else if (group.credentials.blacklisted > 0)
      degraded.push({
        key: 'blocked-' + group.id,
        id: group.id,
        icon: TriangleAlert,
        tone: 'warning',
        subject: group.name,
        detail: t('home.attention.blocked', { count: n(group.credentials.blacklisted) }),
      })
  }
  return [...stalled, ...degraded]
})
const visible = computed(() => items.value.slice(0, visibleLimit))
const overflow = computed(() => items.value.length - visible.value.length)
</script>

<template>
  <AppPanel :title="t('home.attention.title')" compact>
    <template #actions>
      <AppBadge v-if="items.length" tone="warning" size="xs">{{ n(items.length) }}</AppBadge>
      <AppBadge v-else tone="success" size="xs" variant="plain" dot>{{
        t('home.attention.clear')
      }}</AppBadge>
    </template>
    <p v-if="!items.length" class="modern-home-attention-clear">
      <AppIcon :icon="CircleCheck" size="sm" />{{ t('home.attention.clearHelp') }}
    </p>
    <ul v-else class="modern-home-attention">
      <li v-for="item in visible" :key="item.key">
        <RouterLink :to="{ name: 'modern-group-detail', params: { id: item.id } }">
          <AppIcon :icon="item.icon" size="sm" :class="`is-${item.tone}`" />
          <span>
            <strong>{{ item.subject }}</strong>
            <span>{{ item.detail }}</span>
          </span>
          <AppIcon :icon="ChevronRight" size="sm" class="is-quiet" />
        </RouterLink>
      </li>
      <li v-if="overflow" class="modern-home-attention-more">
        <RouterLink :to="{ name: 'modern-health' }">{{
          t('home.attention.more', { count: n(overflow) })
        }}</RouterLink>
      </li>
    </ul>
  </AppPanel>
</template>

<style scoped>
.modern-home-attention-clear {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-attention {
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-home-attention li + li {
  border-top: var(--modern-line-width) solid
    color-mix(in srgb, var(--modern-border) 45%, transparent);
}
.modern-home-attention a {
  display: grid;
  grid-template-columns: var(--modern-icon-sm) minmax(0, 1fr) var(--modern-icon-sm);
  align-items: center;
  gap: var(--modern-space-2);
  min-height: var(--modern-space-10);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-attention a > span {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--modern-space-1) var(--modern-space-2);
  min-width: 0;
}
.modern-home-attention strong {
  overflow: hidden;
  font-weight: var(--modern-weight-medium);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-home-attention a > span > span {
  color: var(--modern-muted);
}
.modern-home-attention .is-danger {
  color: var(--modern-danger);
}
.modern-home-attention .is-warning {
  color: var(--modern-warning);
}
.modern-home-attention .is-quiet {
  color: var(--modern-muted);
}
.modern-home-attention-more {
  padding-top: var(--modern-space-2);
  color: var(--modern-accent);
  font-size: var(--modern-font-size-small);
}
</style>
