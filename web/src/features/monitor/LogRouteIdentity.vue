<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ChannelDto } from '@/app/resources/channels'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import { formatRouteEntity } from './log-format'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'

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
    groupDeleted: false,
    credentialDeleted: false,
    channel: undefined,
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
// 渠道只有一个图标，名称必须靠提示传达。
const channelTooltip = computed(() =>
  props.channelId === null
    ? ''
    : t('monitor.logs.routeIdentity.channel', { name: channelLabel.value }),
)

// 分组、凭据与访问密钥共用同一套名称回退。
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
// 退回编号时用等宽字体，与名称区分开。
const hasGroupName = computed(() => Boolean(props.groupName?.trim()) || props.groupDeleted)
const hasCredentialName = computed(
  () => Boolean(props.credentialName?.trim()) || props.credentialDeleted,
)
const canFilterGroup = computed(() => props.filterable && props.groupId !== null)
const canFilterCredential = computed(() => props.filterable && props.credentialId !== null)

const groupAction = computed(() =>
  t('monitor.logs.routeIdentity.filterGroup', { name: groupLabel.value }),
)
const credentialAction = computed(() =>
  t('monitor.logs.routeIdentity.filterCredential', { name: credentialLabel.value }),
)
</script>

<template>
  <span class="log-route-identity" :class="`log-route-identity--${appearance}`">
    <span class="log-route-identity__line">
      <AppTooltip v-if="showsIcon" :content="channelTooltip">
        <span class="log-route-identity__icon" tabindex="0" :aria-label="channelTooltip">
          <ChannelIcon :icon="channel?.icon ?? ''" :mark="channel?.mark ?? ''" />
        </span>
      </AppTooltip>
      <span v-else-if="channelId !== null" class="log-route-identity__channel">
        {{ channelLabel }}
      </span>

      <OverflowTooltip
        :key="`group-${canFilterGroup}`"
        :as="canFilterGroup ? 'button' : 'span'"
        class="log-route-identity__group"
        :class="{
          'log-route-identity__group--code': !hasGroupName,
          'filterable-value': canFilterGroup,
        }"
        :content="groupLabel"
        :type="canFilterGroup ? 'button' : undefined"
        :aria-label="canFilterGroup ? groupAction : undefined"
        @click="canFilterGroup && emit('filter-group', groupId as number)"
      >
        {{ groupLabel }}
      </OverflowTooltip>
    </span>

    <OverflowTooltip
      v-if="credentialLabel"
      :key="`credential-${canFilterCredential}`"
      :as="canFilterCredential ? 'button' : 'span'"
      class="log-route-identity__credential"
      :class="{
        'log-route-identity__credential--code': !hasCredentialName,
        'filterable-value': canFilterCredential,
      }"
      :content="credentialLabel"
      :type="canFilterCredential ? 'button' : undefined"
      :aria-label="canFilterCredential ? credentialAction : undefined"
      @click="canFilterCredential && emit('filter-credential', credentialId as number)"
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

.log-route-identity__icon:focus-visible {
  border-radius: 3px;
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.log-route-identity__channel {
  flex: none;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}

/* 名称可任意长，一律省略；凭据行独占一行，两者都不挤压彼此。 */
.log-route-identity__group,
.log-route-identity__credential {
  display: block;
  min-width: 0;
  overflow: hidden;
  border: 0;
  background: none;
  padding: 0;
  font: inherit;
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
