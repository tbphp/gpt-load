<script setup lang="ts">
import { ChevronRight, CircleCheck } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HealthReport } from '@modern/api/health'
import { AppBadge, AppButton, AppIcon, AppPanel } from '@modern/components/ui'
import { collectAttention } from './home-attention'

const props = defineProps<{ report?: HealthReport; failed: boolean }>()
defineEmits<{ retry: [] }>()
const { t, n } = useI18n()
/* 首页最多列这么多条，其余折成一行汇总，免得把接入面板顶下去。 */
const visibleLimit = 4
const items = computed(() => collectAttention(props.report))
const visible = computed(() => items.value.slice(0, visibleLimit))
const overflow = computed(() => items.value.length - visible.value.length)
</script>

<template>
  <AppPanel :title="t('home.attention.title')" compact>
    <template #actions>
      <AppBadge v-if="items.length" tone="warning" size="xs">{{ n(items.length) }}</AppBadge>
      <AppBadge v-else-if="report" tone="success" size="xs" variant="plain" dot>{{
        t('home.attention.clear')
      }}</AppBadge>
    </template>
    <div v-if="failed && !report" class="modern-home-attention-state" role="status">
      <span>{{ t('home.attention.failed') }}</span>
      <AppButton size="xs" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-else-if="!report" class="modern-home-attention-state">{{ t('ui.loading') }}</p>
    <p v-else-if="!items.length" class="modern-home-attention-state">
      <AppIcon :icon="CircleCheck" size="sm" />{{ t('home.attention.clearHelp') }}
    </p>
    <ul v-else class="modern-home-attention">
      <li v-for="item in visible" :key="item.key">
        <RouterLink :to="item.to">
          <AppIcon :icon="item.icon" size="sm" :class="`is-${item.tone}`" />
          <span>
            <strong>{{ item.subject }}</strong>
            <span>{{ t('home.attention.' + item.detail, item.params) }}</span>
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
.modern-home-attention-state {
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
/* 只有条目行是三列栅格；「还有 N 项」那条也命中的话，
   文字会被挤进 14px 的图标列，一个字一行。 */
.modern-home-attention li:not(.modern-home-attention-more) a {
  display: grid;
  grid-template-columns: var(--modern-icon-sm) minmax(0, 1fr) var(--modern-icon-sm);
  align-items: center;
  gap: var(--modern-space-2);
  min-height: var(--modern-space-10);
  font-size: var(--modern-font-size-secondary);
}
/* 分组名是要动的对象，优先保住；两边都能收缩，但说明文字收缩得快得多，
   所以先被截的是说明而不是名字。 */
.modern-home-attention a > span {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-home-attention strong {
  overflow: hidden;
  min-width: 0;
  flex: 0 1 auto;
  font-weight: var(--modern-weight-medium);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-home-attention a > span > span {
  overflow: hidden;
  min-width: 0;
  flex: 0 6 auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  text-overflow: ellipsis;
  white-space: nowrap;
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
