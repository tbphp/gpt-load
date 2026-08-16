<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ChannelDto } from '@/app/resources/channels'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import AppTooltip from '@/components/ui/AppTooltip.vue'

const props = defineProps<{
  groupId: number | null
  groupName?: string | null
  channelId: string | null
  channel?: Pick<ChannelDto, 'name' | 'icon' | 'mark'> | null
  credentialId: number | null
}>()

const { t } = useI18n()

// The icon is what carries the channel identity, so it replaces the channel ID
// in the label. Callers that pass no icon metadata — or a channel list that
// failed to load — must keep the ID, otherwise the row only shows G/K.
const showsIcon = computed(() => Boolean(props.channel?.mark))

const compactLabel = computed(() => {
  const parts: string[] = []
  if (props.groupId !== null) parts.push(`G${props.groupId}`)
  if (props.credentialId !== null) parts.push(`K${props.credentialId}`)
  if (!showsIcon.value && props.channelId !== null) parts.push(props.channelId)
  return parts.join('·') || '—'
})

const tooltip = computed(() => {
  const lines: string[] = []
  if (props.groupId !== null) {
    lines.push(
      t('monitor.logs.routeIdentity.group', {
        name: props.groupName?.trim() || '—',
        id: props.groupId,
      }),
    )
  }
  if (props.channelId !== null) {
    lines.push(
      t('monitor.logs.routeIdentity.channel', {
        name: props.channel?.name.trim() || '—',
        id: props.channelId,
      }),
    )
  }
  if (props.credentialId !== null) {
    lines.push(t('monitor.logs.routeIdentity.credential', { id: props.credentialId }))
  }
  return lines.join('\n')
})
</script>

<template>
  <AppTooltip :content="tooltip" :disabled="tooltip.length === 0" side="top" align="start">
    <span
      class="log-route-identity"
      :tabindex="tooltip ? 0 : undefined"
      :aria-label="tooltip || undefined"
    >
      <ChannelIcon
        v-if="showsIcon"
        class="log-route-identity__icon"
        :icon="channel?.icon ?? ''"
        :mark="channel?.mark ?? ''"
      />
      <code class="log-route-identity__label">{{ compactLabel }}</code>
    </span>
  </AppTooltip>
</template>

<style scoped>
.log-route-identity {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 4px;
  cursor: help;
}

.log-route-identity:focus-visible {
  border-radius: var(--radius-tag);
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}

.log-route-identity__icon {
  flex: none;
  font-size: var(--text-label-xs);
}

.log-route-identity__label {
  overflow: hidden;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
