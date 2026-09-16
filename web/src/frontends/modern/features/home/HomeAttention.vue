<script setup lang="ts">
import { ArrowRight, CircleCheck } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { HealthReport } from '@modern/api/health'
import { AppBadge, AppButton, AppIcon, AppOverflowText, AppPanel } from '@modern/components/ui'
import { collectAttention, type AttentionItem } from './home-attention'

const props = defineProps<{ report?: HealthReport; failed: boolean }>()
defineEmits<{ retry: [] }>()
const { t, n } = useI18n()
const items = computed(() => collectAttention(props.report))
function detail(item: AttentionItem): string {
  const params = Object.fromEntries(
    Object.entries(item.params).map(([key, value]) => [
      key,
      typeof value === 'number' ? n(value) : value,
    ]),
  )
  return t('home.attention.' + item.detail, params)
}
</script>

<template>
  <AppPanel :title="t('home.attention.title')" compact>
    <template #actions>
      <AppBadge v-if="items.length" tone="warning" size="xs" class="modern-home-attention-count">{{
        n(items.length)
      }}</AppBadge>
      <AppBadge v-else-if="report && !failed" tone="success" size="xs" variant="plain" dot>{{
        t('home.attention.clear')
      }}</AppBadge>
    </template>
    <div v-if="failed" class="modern-home-attention-state" role="status">
      <span>{{ t(report ? 'home.refreshFailed' : 'home.attention.failed') }}</span>
      <AppButton size="xs" variant="text" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-if="!report && !failed" class="modern-home-attention-state">{{ t('ui.loading') }}</p>
    <p v-else-if="report && !items.length && !failed" class="modern-home-attention-state">
      <AppIcon :icon="CircleCheck" size="sm" />{{ t('home.attention.clearHelp') }}
    </p>
    <ul v-if="items.length" class="modern-home-attention">
      <li v-for="item in items" :key="item.key">
        <RouterLink :to="item.to" class="modern-home-attention-link">
          <AppIcon :icon="item.icon" size="sm" :class="'is-' + item.tone" />
          <span class="modern-home-attention-copy">
            <AppOverflowText class="modern-home-attention-subject" :text="item.subject" />
            <AppOverflowText class="modern-home-attention-detail" :text="detail(item)" />
          </span>
          <AppIcon :icon="ArrowRight" size="sm" class="modern-home-attention-arrow" />
        </RouterLink>
      </li>
    </ul>
  </AppPanel>
</template>

<style scoped>
.modern-home-attention-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-body);
}
.modern-home-attention {
  overflow-y: auto;
  max-height: 176px;
  margin: 0;
  padding: 0;
  list-style: none;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
}
.modern-home-attention-count {
  min-width: var(--modern-control-xs);
  min-height: var(--modern-control-xs);
  justify-content: center;
  border-radius: var(--modern-radius-round);
}
.modern-home-attention-state + .modern-home-attention {
  margin-top: var(--modern-space-2);
}
.modern-home-attention li + li {
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-home-attention-link {
  display: grid;
  grid-template-columns: var(--modern-icon-sm) minmax(0, 1fr) var(--modern-icon-sm);
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  min-height: 44px;
  border-radius: var(--modern-radius-small);
  padding: var(--modern-space-2) var(--modern-space-1);
  color: var(--modern-text);
}
.modern-home-attention-link:hover {
  background: var(--modern-control-hover);
}
.modern-home-attention-copy {
  display: flex;
  align-items: baseline;
  gap: var(--modern-space-1);
  min-width: 0;
}
.modern-home-attention-subject {
  flex: 0 1 auto;
  max-width: 54%;
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-home-attention-detail {
  flex: 1;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-home-attention .is-danger {
  color: var(--modern-danger);
}
.modern-home-attention .is-warning {
  color: var(--modern-warning);
}
.modern-home-attention-arrow {
  color: var(--modern-muted);
}
.modern-home-attention-link:hover .modern-home-attention-arrow,
.modern-home-attention-link:hover .modern-home-attention-subject {
  color: var(--modern-accent);
}
</style>
