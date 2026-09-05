<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ChannelDto } from '@/app/resources/channels'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'

const props = withDefaults(
  defineProps<{
    groupId: number | null
    groupName?: string | null
    channelId: string | null
    channel?: Pick<ChannelDto, 'name' | 'icon' | 'mark'> | null
    credentialId: number | null
    appearance?: 'compact' | 'plain'
    /** 列表里点分组名就地筛选；详情抽屉没有筛选上下文，保持纯展示。 */
    filterable?: boolean
  }>(),
  {
    groupName: undefined,
    channel: undefined,
    appearance: 'compact',
    filterable: false,
  },
)

const emit = defineEmits<{ 'filter-group': [groupId: number] }>()
const { t } = useI18n()

// 图标优先表达渠道；图标资源缺失时退回渠道名，ID 是最后的诊断兜底。
const showsIcon = computed(() => Boolean(props.channel?.mark))
const channelLabel = computed(() => props.channel?.name.trim() || props.channelId || '—')

// 分组名是这一列最有价值的信息，取不到（分组已删除）才退回编号。
const groupLabel = computed(() => {
  const name = props.groupName?.trim()
  if (name) return name
  return props.groupId === null ? '—' : `G${props.groupId}`
})
const hasGroupName = computed(() => Boolean(props.groupName?.trim()))
const canFilter = computed(() => props.filterable && props.groupId !== null)
const credentialLabel = computed(() =>
  props.credentialId === null ? '' : `K${props.credentialId}`,
)

const groupTooltip = computed(() => {
  if (props.groupId === null) return ''
  const identity = t('monitor.logs.routeIdentity.group', {
    name: props.groupName?.trim() || '—',
    id: props.groupId,
  })
  return canFilter.value ? `${identity}\n${t('monitor.logs.routeIdentity.filterHint')}` : identity
})

const credentialTooltip = computed(() =>
  props.credentialId === null
    ? ''
    : t('monitor.logs.routeIdentity.credential', { id: props.credentialId }),
)

// plain 外观（详情抽屉）横向空间充裕，把渠道也摊开说清楚。
const plainChannelTooltip = computed(() =>
  props.channelId === null
    ? ''
    : t('monitor.logs.routeIdentity.channel', { name: channelLabel.value }),
)
</script>

<template>
  <span class="log-route-identity" :class="`log-route-identity--${appearance}`">
    <span class="log-route-identity__line">
      <AppTooltip v-if="showsIcon && plainChannelTooltip" :content="plainChannelTooltip">
        <span class="log-route-identity__icon" tabindex="0" :aria-label="plainChannelTooltip">
          <ChannelIcon :icon="channel?.icon ?? ''" :mark="channel?.mark ?? ''" />
        </span>
      </AppTooltip>
      <span v-else-if="!showsIcon && channelId !== null" class="log-route-identity__channel">
        {{ channelLabel }}
      </span>

      <AppTooltip :content="groupTooltip" :disabled="groupTooltip.length === 0">
        <button
          v-if="canFilter"
          class="log-route-identity__group log-route-identity__group--action"
          type="button"
          :aria-label="groupTooltip || undefined"
          @click="emit('filter-group', groupId as number)"
        >
          {{ groupLabel }}
        </button>
        <span
          v-else
          class="log-route-identity__group"
          :class="{ 'log-route-identity__group--code': !hasGroupName }"
          :tabindex="groupTooltip ? 0 : undefined"
          :aria-label="groupTooltip || undefined"
        >
          {{ groupLabel }}
        </span>
      </AppTooltip>
    </span>

    <OverflowTooltip
      v-if="credentialLabel"
      as="span"
      class="log-route-identity__credential"
      :content="credentialTooltip"
    >
      {{ credentialLabel }}
    </OverflowTooltip>
  </span>
</template>

<style scoped>
.log-route-identity {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.log-route-identity__line {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 4px;
}

.log-route-identity__icon {
  display: inline-flex;
  flex: none;
  font-size: var(--text-label-xs);
}

.log-route-identity__icon:focus-visible,
.log-route-identity__group:focus-visible {
  border-radius: 3px;
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.log-route-identity__channel {
  flex: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

/* 分组名可任意长，一律省略；宽度不够时优先保住它，凭据号不参与收缩。 */
.log-route-identity__group {
  min-width: 0;
  overflow: hidden;
  border: 0;
  background: none;
  color: var(--color-text);
  padding: 0;
  font: inherit;
  font-size: var(--text-sm);
  font-weight: 560;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-route-identity__group--code {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
}

.log-route-identity__group--action {
  cursor: pointer;
}

.log-route-identity__group--action:hover {
  color: var(--color-action);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.log-route-identity__credential {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 抽屉里横向空间充裕，排成一行并跟随所在字段的字号。 */
.log-route-identity--plain {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 8px;
}

.log-route-identity--plain .log-route-identity__icon,
.log-route-identity--plain .log-route-identity__group,
.log-route-identity--plain .log-route-identity__credential,
.log-route-identity--plain .log-route-identity__channel {
  font-size: inherit;
}

.log-route-identity--plain .log-route-identity__credential {
  color: var(--color-text-muted);
}
</style>
