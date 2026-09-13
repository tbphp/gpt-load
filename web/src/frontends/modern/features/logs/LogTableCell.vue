<script setup lang="ts">
import { KeyRound, UserRound } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LogEntry } from '@modern/api/logs'
import type { GroupRow } from '@modern/api/groups'
import type { GroupChannel } from '@modern/api/group-create'
import { AppButton, AppChannelIcon, AppIcon, AppOverflowText } from '@modern/components/ui'
import type { LogColumnId } from './log-columns'
import { logTime } from './log-display'
import LogValue from './LogValue.vue'

const props = defineProps<{
  row: LogEntry
  fields: readonly LogColumnId[]
  groups: ReadonlyMap<number, GroupRow>
  channels: ReadonlyMap<string, GroupChannel>
}>()
defineEmits<{ open: [] }>()
const { t, locale } = useI18n()
const paired = computed(() => props.fields.length > 1)
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
  new Intl.DateTimeFormat(locale.value, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(props.row.completed_at_ms),
)
const date = computed(() =>
  new Intl.DateTimeFormat(locale.value, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(props.row.completed_at_ms),
)
const labeledFields: readonly LogColumnId[] = [
  'stream',
  'attempt_count',
  'first_response_ms',
  'duration_ms',
  'input_tokens',
  'output_tokens',
]
function identityIcon(field: LogColumnId) {
  return field === 'credential_name' ? UserRound : field === 'access_key' ? KeyRound : undefined
}
</script>

<template>
  <AppButton
    v-if="fields[0] === 'completed_at_ms'"
    variant="text"
    class="modern-log-time"
    @click="$emit('open')"
  >
    <AppOverflowText :text="clock" :full-text="logTime(row.completed_at_ms, locale, true)" />
    <span>{{ date }}</span>
  </AppButton>
  <div v-else-if="routing" class="modern-log-routing-cell">
    <div v-if="channelIdentity" class="modern-log-routing-mark">
      <AppChannelIcon
        :icon="channelIdentity.icon"
        :name="channelIdentity.name"
        :mark="channelIdentity.mark"
        size="sm"
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
  <div
    v-else
    class="modern-log-cell-stack"
    :class="{
      'is-paired': paired,
      'has-labels': paired && fields.some((field) => labeledFields.includes(field)),
    }"
  >
    <template v-for="field in fields" :key="field">
      <span v-if="paired && labeledFields.includes(field)" class="modern-log-cell-label">{{
        t('logs.shortColumns.' + field)
      }}</span>
      <div
        class="modern-log-cell-value"
        :class="{
          'is-model': ['client_model', 'upstream_model', 'upstream_reported_model'].includes(field),
          'is-protocol': field === 'protocol' || field === 'upstream_protocol',
        }"
      >
        <AppIcon
          v-if="identityIcon(field)"
          :icon="identityIcon(field)!"
          :label="t('logs.columns.' + field)"
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
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
  gap: var(--modern-space-1);
  max-width: 100%;
  font-size: inherit;
  line-height: var(--modern-leading-compact);
  font-variant-numeric: tabular-nums;
}
.modern-log-time > :first-child {
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
.modern-log-time > :last-child {
  color: var(--modern-muted);
  font-weight: var(--modern-weight-regular);
}
.modern-log-time:hover > :first-child {
  color: var(--modern-accent);
}
.modern-log-cell-stack,
.modern-log-routing-lines {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-1-5);
  align-content: center;
}
.modern-log-cell-stack.has-labels {
  grid-template-columns: max-content minmax(0, 1fr);
  align-items: center;
  gap: var(--modern-space-1-5) var(--modern-space-2);
}
.modern-log-cell-label {
  color: var(--modern-muted);
  font-size: inherit;
  line-height: var(--modern-leading-compact);
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
  font-weight: var(--modern-weight-medium);
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
  width: var(--modern-control-sm);
  height: var(--modern-control-sm);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
}
.modern-log-routing-name {
  font-weight: var(--modern-weight-medium);
}
.modern-log-secondary-line {
  color: var(--modern-muted);
}
</style>
