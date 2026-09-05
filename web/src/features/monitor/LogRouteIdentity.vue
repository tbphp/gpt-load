<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ChannelDto } from '@/app/resources/channels'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'

import { formatRouteEntity } from './log-format'

const props = withDefaults(
  defineProps<{
    groupId: number | null
    groupName?: string | null
    channelId: string | null
    channel?: Pick<ChannelDto, 'name' | 'icon' | 'mark'> | null
    credentialId: number | null
    credentialName?: string | null
    /** 名称来源已就绪却查不到，即该实体已被删除。 */
    groupDeleted?: boolean
    credentialDeleted?: boolean
    appearance?: 'compact' | 'plain'
    /** 列表里点分组、凭据就地筛选；详情抽屉没有筛选上下文，保持纯展示。 */
    filterable?: boolean
  }>(),
  {
    groupName: undefined,
    credentialName: undefined,
    channel: undefined,
    groupDeleted: false,
    credentialDeleted: false,
    appearance: 'compact',
    filterable: false,
  },
)

const emit = defineEmits<{
  'filter-group': [groupId: number]
  'filter-credential': [credentialId: number]
}>()
const { t } = useI18n()

// 图标优先表达渠道；图标资源缺失时退回渠道名，ID 是最后的诊断兜底。
const showsIcon = computed(() => Boolean(props.channel?.mark))
const channelLabel = computed(() => props.channel?.name.trim() || props.channelId || '—')

// 取不到名称（已删除）才退回编号，凭据与分组同一套规则。
const groupLabel = computed(() =>
  formatRouteEntity({
    id: props.groupId,
    name: props.groupName,
    deleted: props.groupDeleted,
    prefix: 'G',
    deletedText: (id) => t('monitor.logs.deletedRef', { id }),
  }),
)
const credentialLabel = computed(() =>
  props.credentialId === null
    ? ''
    : formatRouteEntity({
        id: props.credentialId,
        name: props.credentialName,
        deleted: props.credentialDeleted,
        prefix: 'K',
        deletedText: (id) => t('monitor.logs.deletedRef', { id }),
      }),
)
const hasGroupName = computed(() => Boolean(props.groupName?.trim()) || props.groupDeleted)
const hasCredentialName = computed(
  () => Boolean(props.credentialName?.trim()) || props.credentialDeleted,
)
const canFilterGroup = computed(() => props.filterable && props.groupId !== null)
const canFilterCredential = computed(() => props.filterable && props.credentialId !== null)

// 三个字段共用一条提示：列里放不下的名称在这里给全，省得逐个悬停。
const routeTooltip = computed(() => {
  const lines: string[] = []
  if (props.channelId !== null) {
    lines.push(t('monitor.logs.routeIdentity.channel', { name: channelLabel.value }))
  }
  if (props.groupId !== null) {
    lines.push(t('monitor.logs.routeIdentity.group', { name: groupLabel.value }))
  }
  if (credentialLabel.value) {
    lines.push(t('monitor.logs.routeIdentity.credential', { name: credentialLabel.value }))
  }
  return lines.join('\n')
})

const groupAction = computed(() =>
  t('monitor.logs.routeIdentity.filterGroup', { name: groupLabel.value }),
)
const credentialAction = computed(() =>
  t('monitor.logs.routeIdentity.filterCredential', { name: credentialLabel.value }),
)
</script>

<template>
  <!-- 抽屉里字段已完整展开，无需再提示。 -->
  <AppTooltip
    :content="routeTooltip"
    :disabled="appearance === 'plain' || routeTooltip.length === 0"
    side="top"
    align="start"
  >
    <span class="log-route-identity" :class="`log-route-identity--${appearance}`">
      <span class="log-route-identity__line">
        <span v-if="showsIcon" class="log-route-identity__icon">
          <ChannelIcon :icon="channel?.icon ?? ''" :mark="channel?.mark ?? ''" />
        </span>
        <span v-else-if="channelId !== null" class="log-route-identity__channel">
          {{ channelLabel }}
        </span>

        <button
          v-if="canFilterGroup"
          class="log-route-identity__group filterable-value"
          :class="{ 'log-route-identity__group--code': !hasGroupName }"
          type="button"
          :aria-label="groupAction"
          @click="emit('filter-group', groupId as number)"
        >
          {{ groupLabel }}
        </button>
        <span
          v-else
          class="log-route-identity__group"
          :class="{ 'log-route-identity__group--code': !hasGroupName }"
        >
          {{ groupLabel }}
        </span>
      </span>

      <template v-if="credentialLabel">
        <button
          v-if="canFilterCredential"
          class="log-route-identity__credential filterable-value"
          :class="{ 'log-route-identity__credential--code': !hasCredentialName }"
          type="button"
          :aria-label="credentialAction"
          @click="emit('filter-credential', credentialId as number)"
        >
          {{ credentialLabel }}
        </button>
        <span
          v-else
          class="log-route-identity__credential"
          :class="{ 'log-route-identity__credential--code': !hasCredentialName }"
        >
          {{ credentialLabel }}
        </span>
      </template>
    </span>
  </AppTooltip>
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

.log-route-identity__channel {
  flex: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

/* 名称可任意长，一律省略；凭据独占一行，两者不挤压彼此。 */
.log-route-identity__group,
.log-route-identity__credential {
  display: block;
  min-width: 0;
  overflow: hidden;
  border: 0;
  background: none;
  padding: 0;
  font: inherit;
  /* 行高须高于字号，否则悬停下划线被 overflow 裁掉；须排在 font 简写之后。 */
  line-height: 1.5;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-route-identity__group {
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 560;
}

.log-route-identity__credential {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

.log-route-identity__group--code,
.log-route-identity__credential--code {
  font-family: var(--font-mono);
  font-variant-numeric: tabular-nums;
}

.log-route-identity__group--code {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

/* 下方两条规则设了字色，与全局悬停色特异性相同却后加载，这里叠类名压过它们。 */
.log-route-identity__group.filterable-value:hover,
.log-route-identity__credential.filterable-value:hover {
  color: var(--color-action);
}

/* 抽屉空间充裕，排成一行并跟随所在字段字号。 */
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
