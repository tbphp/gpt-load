<script setup lang="ts">
import { ChevronRight } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import type { ModelSource } from '@modern/api/models'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppIcon,
  AppOverflowText,
  AppTooltip,
} from '@modern/components/ui'

defineProps<{
  source: ModelSource
  requestModel?: string
  allGroups?: boolean
  disabled?: boolean
}>()
defineEmits<{ open: [] }>()
const { t, n } = useI18n()
</script>

<template>
  <AppButton
    variant="ghost"
    class="modern-model-source"
    :class="{ 'shows-all-groups': allGroups }"
    :disabled="disabled"
    @click="$emit('open')"
  >
    <AppChannelIcon
      :icon="source.price.channel.icon"
      :mark="source.price.channel.mark"
      :name="source.price.channel.name"
      :tooltip="false"
      size="sm"
      class="modern-model-source-mark"
    />
    <span class="modern-model-source-content">
      <span class="modern-model-source-heading">
        <span class="modern-model-source-channel">
          <AppOverflowText :text="source.price.channel.name || t('logs.deleted')" />
        </span>
        <AppBadge v-if="source.price.status === 'pending'" tone="warning" variant="plain" size="xs">
          {{ t('modelManager.pending') }}
        </AppBadge>
      </span>
      <AppOverflowText
        v-if="source.model !== requestModel"
        :text="source.model"
        class="modern-model-source-name"
      />
      <span v-if="source.groups.length" class="modern-model-source-groups">
        <AppBadge
          v-for="group in allGroups ? source.groups : source.groups.slice(0, 2)"
          :key="group.id"
          :class="{ 'is-disabled': !group.enabled }"
          size="xs"
          tone="neutral"
        >
          <AppOverflowText :text="group.name || t('logs.deleted')" />
        </AppBadge>
        <AppTooltip
          v-if="!allGroups && source.groups.length > 2"
          :label="
            source.groups
              .slice(2)
              .map((group) => group.name || t('logs.deleted'))
              .join('\n')
          "
        >
          <span class="modern-model-source-more">+{{ n(source.groups.length - 2) }}</span>
        </AppTooltip>
      </span>
    </span>
    <AppIcon :icon="ChevronRight" size="xs" class="modern-model-source-arrow" />
  </AppButton>
</template>

<style scoped>
.modern-model-source {
  display: grid;
  grid-template-columns: var(--modern-channel-sm) minmax(0, 1fr) var(--modern-icon-xs);
  align-items: center;
  gap: var(--modern-space-2);
  width: 100%;
  min-width: 0;
  padding: var(--modern-space-2) var(--modern-space-1);
  text-align: left;
}
.modern-model-source-mark {
  align-self: start;
}
.modern-model-source-content {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-1) var(--modern-space-2);
}
.modern-model-source-heading {
  display: flex;
  align-items: center;
  min-width: 0;
  max-width: 100%;
  gap: var(--modern-space-2);
}
.modern-model-source-channel {
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  line-height: var(--modern-leading-compact);
}
.modern-model-source-name {
  flex-basis: 100%;
  order: 1;
  min-width: 0;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-family: var(--modern-font-mono);
  font-weight: var(--modern-weight-regular);
}
.modern-model-source-groups {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-width: 0;
  max-width: 100%;
}
.modern-model-source-groups > :not(.modern-model-source-more) {
  min-width: 0;
  flex: 0 1 auto;
}
.shows-all-groups .modern-model-source-groups {
  flex-wrap: wrap;
}
.modern-model-source-groups > .is-disabled {
  opacity: var(--modern-opacity-quiet);
}
.modern-model-source-more {
  flex: none;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  font-variant-numeric: tabular-nums;
}
.modern-model-source-arrow {
  color: var(--modern-control-placeholder);
}
.modern-model-source:hover .modern-model-source-arrow,
.modern-model-source:focus-visible .modern-model-source-arrow {
  color: var(--modern-accent);
}
</style>
