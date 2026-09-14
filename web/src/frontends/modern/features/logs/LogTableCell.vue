<script setup lang="ts">
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import { KeyRound, UserRound } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LogEntry } from '@modern/api/logs'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import {
  AppButton,
  AppChannelIcon,
  AppIcon,
  AppOverflowText,
  AppProtocolTag,
  AppTooltip,
} from '@modern/components/ui'
import type { LogColumnId } from './log-columns'
import { logTime } from './log-display'
import LogValue from './LogValue.vue'
import LogModelWarning from './LogModelWarning.vue'

const props = defineProps<{
  row: LogEntry
  fields: readonly LogColumnId[]
  peer?: boolean
  groups: ReadonlyMap<number, GroupRow>
  channels: ReadonlyMap<string, GroupChannel>
}>()
defineEmits<{ open: [] }>()
const { t, locale } = useI18n()
const paired = computed(() => props.fields.length > 1)
const protocolColumn = computed(() => props.fields.length === 1 && props.fields[0] === 'protocol')
const modelColumn = computed(() => props.fields.length === 1 && props.fields[0] === 'client_model')
// 同值合并，缺值只展示已有信息；没有上游记录不代表发生转换。
const identityLines = computed(() => {
  const request = protocolColumn.value ? props.row.protocol : props.row.client_model
  const upstream = protocolColumn.value ? props.row.upstream_protocol : props.row.upstream_model
  const kind = protocolColumn.value ? 'protocol' : 'model'
  if (!request || !upstream || request === upstream) {
    const value = request || upstream
    return value ? [{ value, label: undefined }] : []
  }
  return [
    ...(request ? [{ value: request, label: t(`logs.identityHints.${kind}Request`) }] : []),
    ...(upstream ? [{ value: upstream, label: t(`logs.identityHints.${kind}Upstream`) }] : []),
  ]
})
const routing = computed(() => props.fields.includes('group'))
const group = computed(() =>
  props.row.group_id ? props.groups.get(props.row.group_id) : undefined,
)
const channel = computed(() =>
  props.row.channel_id ? props.channels.get(props.row.channel_id) : undefined,
)
const channelIdentity = computed(() =>
  channel.value
    ? { icon: channel.value.icon, name: channel.value.name, mark: channel.value.mark }
    : group.value
      ? {
          icon: group.value.channelIcon,
          name: group.value.channelName,
          mark: group.value.channelMark,
        }
      : undefined,
)
const clock = computed(() =>
  dateFormatter(locale.value, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(props.row.completed_at_ms),
)
const date = computed(() =>
  dateFormatter(locale.value, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(props.row.completed_at_ms),
)
function identityIcon(field: LogColumnId) {
  return field === 'credential_name' ? UserRound : field === 'access_key' ? KeyRound : undefined
}
</script>

<template>
  <div v-if="fields[0] === 'completed_at_ms'" class="modern-log-cell-stack">
    <AppButton variant="text" class="modern-log-time" @click="$emit('open')">
      <AppOverflowText :text="clock" :full-text="logTime(row.completed_at_ms, locale, true)" />
    </AppButton>
    <div class="modern-log-cell-value is-secondary">
      <span>{{ date }}</span>
    </div>
  </div>
  <div v-else-if="protocolColumn || modelColumn" class="modern-log-identity-cell">
    <div class="modern-log-identity-values">
      <template v-for="(line, index) in identityLines" :key="line.value">
        <AppTooltip v-if="protocolColumn" :label="line.label">
          <span
            class="modern-log-identity-line"
            :tabindex="line.label ? 0 : undefined"
            :aria-label="line.label"
          >
            <AppProtocolTag :protocol="line.value" />
          </span>
        </AppTooltip>
        <div v-else class="modern-log-model-line">
          <AppOverflowText
            class="modern-log-identity-line is-model"
            :text="line.value"
            :hint="line.label"
            :full-text="line.label ? line.label + '\n' + line.value : line.value"
            tabindex="0"
            :aria-label="line.label ? line.label + ' ' + line.value : undefined"
          />
          <LogModelWarning v-if="index === 0" :row="row" />
        </div>
      </template>
      <span v-if="!identityLines.length">—</span>
    </div>
  </div>
  <div v-else-if="routing" class="modern-log-routing-cell">
    <div v-if="channelIdentity" class="modern-log-routing-mark">
      <AppChannelIcon
        :icon="channelIdentity.icon"
        :name="channelIdentity.name"
        :mark="channelIdentity.mark"
        size="sm"
        :tooltip="false"
      />
    </div>
    <div class="modern-log-routing-lines">
      <div
        v-for="field in fields"
        :key="field"
        :class="{
          'modern-log-routing-name': field === 'group',
          'modern-log-secondary-line': field === 'channel',
        }"
      >
        <LogValue
          :row="row"
          :column="field"
          :groups="groups"
          :channels="channels"
          table
          hide-icon
        />
      </div>
    </div>
  </div>
  <div v-else class="modern-log-cell-stack" :class="{ 'is-paired': paired }">
    <template v-for="(field, index) in fields" :key="field">
      <div
        class="modern-log-cell-value"
        :class="{
          'is-secondary': paired && index > 0 && !peer,
          'is-model': ['client_model', 'upstream_model', 'upstream_reported_model'].includes(field),
          'is-protocol': field === 'protocol' || field === 'upstream_protocol',
        }"
      >
        <AppIcon
          v-if="identityIcon(field)"
          :icon="identityIcon(field)!"
          size="sm"
          class="modern-log-identity-icon"
        />
        <LogValue :row="row" :column="field" :groups="groups" :channels="channels" table />
      </div>
    </template>
  </div>
</template>

<style scoped>
.modern-log-time {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  max-width: 100%;
  font-size: inherit;
  line-height: var(--modern-leading-compact);
  font-variant-numeric: tabular-nums;
}
.modern-log-time {
  color: var(--modern-text);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-log-time:hover {
  color: var(--modern-accent);
}
.modern-log-cell-stack,
.modern-log-routing-lines,
.modern-log-identity-values {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-0-5);
  align-content: center;
}
.modern-log-identity-cell {
  min-width: 0;
}
.modern-log-model-line {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-1-5);
  font-size: var(--modern-font-size-small);
}
.modern-log-identity-line {
  display: flex;
  min-width: 0;
  max-width: 100%;
  width: fit-content;
  align-items: center;
}
.modern-log-identity-line.is-model {
  display: block;
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-log-identity-line > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-log-identity-line:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: var(--modern-focus-offset);
}
/* 配对单元格的第二行是附属信息，降一档字号与色阶。 */
.modern-log-cell-value.is-secondary {
  color: var(--modern-control-placeholder);
  font-size: var(--modern-font-size-caption);
}
.modern-log-cell-value {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1-5);
  min-width: 0;
  min-height: var(--modern-badge-xs);
  font-size: inherit;
  line-height: var(--modern-leading-compact);
}
.modern-log-cell-value > :last-child {
  min-width: 0;
}
.modern-log-cell-value.is-model {
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-log-cell-value.is-secondary.is-model {
  font-size: var(--modern-font-size-caption);
  font-weight: var(--modern-weight-regular);
}
.modern-log-identity-icon {
  color: var(--modern-muted);
}
.modern-log-routing-cell {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  min-width: 0;
}
.modern-log-routing-mark {
  display: flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: var(--modern-control-xxs);
  height: var(--modern-control-xxs);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
}
.modern-log-routing-name {
  font-weight: var(--modern-weight-medium);
}
.modern-log-secondary-line {
  color: var(--modern-control-placeholder);
  font-size: var(--modern-font-size-caption);
}
</style>
