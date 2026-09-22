<script setup lang="ts">
import {
  ArrowRight,
  CircleCheck,
  CircleSlash,
  Clock,
  Coins,
  KeyRound,
  TriangleAlert,
} from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import type { GroupRow } from '@modern/api/groups'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppIcon,
  AppOverflowText,
  AppPanel,
} from '@modern/components/ui'
import type { HealthIssue } from '@modern/features/health/health-display'

const props = defineProps<{
  issues?: readonly HealthIssue[]
  groups: ReadonlyMap<number, GroupRow>
  failed: boolean
}>()
defineEmits<{ retry: [] }>()
const { t, n } = useI18n()
const items = computed(() => props.issues ?? [])
const visibleItems = computed(() => items.value.slice(0, 3))
function icon(item: HealthIssue) {
  if (item.kind === 'group') return CircleSlash
  if (item.kind === 'cooldown' || item.kind === 'credit') return Clock
  if (item.kind === 'quota') return Coins
  if (item.kind === 'access_key') return KeyRound
  return TriangleAlert
}
</script>

<template>
  <AppPanel :title="t('home.attention.title')" compact>
    <template #actions>
      <AppBadge v-if="items.length" tone="warning" size="xs" class="modern-home-attention-count">{{
        n(items.length)
      }}</AppBadge>
      <AppBadge v-else-if="issues && !failed" tone="success" size="xs" variant="plain" dot>{{
        t('home.attention.clear')
      }}</AppBadge>
    </template>
    <div v-if="failed" class="modern-home-attention-state" role="status">
      <span>{{ t(issues ? 'home.refreshFailed' : 'home.attention.failed') }}</span>
      <AppButton size="xs" variant="text" @click="$emit('retry')">{{ t('ui.retry') }}</AppButton>
    </div>
    <p v-if="!issues && !failed" class="modern-home-attention-state">{{ t('ui.loading') }}</p>
    <p v-else-if="issues && !items.length && !failed" class="modern-home-attention-state">
      <AppIcon :icon="CircleCheck" size="sm" />{{ t('home.attention.clearHelp') }}
    </p>
    <ul v-if="items.length" class="modern-home-attention">
      <li v-for="item in visibleItems" :key="item.key">
        <RouterLink
          :to="{ name: 'modern-health', query: { detail: item.key } }"
          class="modern-home-attention-link"
        >
          <AppChannelIcon
            v-if="
              item.identityType === 'credential' && item.groupID && props.groups.get(item.groupID)
            "
            :icon="props.groups.get(item.groupID)!.channelIcon"
            :mark="props.groups.get(item.groupID)!.channelMark"
            :name="props.groups.get(item.groupID)!.channelName"
            :group-name="item.groupName"
            size="sm"
          />
          <AppIcon v-else :icon="icon(item)" size="sm" :class="'is-' + item.severity" />
          <span class="modern-home-attention-copy">
            <AppOverflowText class="modern-home-attention-subject" :text="item.name" />
            <AppOverflowText class="modern-home-attention-detail" :text="item.reason" />
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
  margin: 0;
  padding: 0;
  list-style: none;
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
  grid-template-columns: var(--modern-channel-sm) minmax(0, 1fr) var(--modern-icon-sm);
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
  min-height: var(--modern-control-md);
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
