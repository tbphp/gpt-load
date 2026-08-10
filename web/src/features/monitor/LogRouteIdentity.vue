<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import AppTooltip from '@/components/ui/AppTooltip.vue'

const props = defineProps<{
  groupId: number | null
  groupName?: string | null
  channelId: string | null
  channelName?: string | null
  credentialId: number | null
}>()

const { t } = useI18n()

const compactLabel = computed(() => {
  const parts: string[] = []
  if (props.groupId !== null) parts.push(`G${props.groupId}`)
  if (props.credentialId !== null) parts.push(`K${props.credentialId}`)
  if (props.channelId !== null) parts.push(props.channelId)
  return parts.join(' · ') || '—'
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
        name: props.channelName?.trim() || '—',
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
    <code
      class="log-route-identity"
      :tabindex="tooltip ? 0 : undefined"
      :aria-label="tooltip || undefined"
    >
      {{ compactLabel }}
    </code>
  </AppTooltip>
</template>

<style scoped>
.log-route-identity {
  display: block;
  min-width: 0;
  overflow: hidden;
  color: var(--color-text-faint);
  cursor: help;
  font-size: var(--text-label-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-route-identity:focus-visible {
  border-radius: var(--radius-tag);
  outline: 2px solid var(--color-focus);
  outline-offset: 2px;
}
</style>
