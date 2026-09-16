<script setup lang="ts">
import { ChevronRight, CircleCheck } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { GroupRow } from '@modern/api/groups'
import { AppBadge, AppIcon, AppPanel } from '@modern/components/ui'
import { collectAttention } from './home-attention'

const props = defineProps<{ groups: GroupRow[] }>()
const { t, n } = useI18n()
/* 首页最多列这么多条，其余折成一行汇总，免得把接入面板顶下去。 */
const visibleLimit = 4
const items = computed(() => collectAttention(props.groups))
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
            <span>{{ t('home.attention.' + item.detail, { count: n(item.count) }) }}</span>
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
