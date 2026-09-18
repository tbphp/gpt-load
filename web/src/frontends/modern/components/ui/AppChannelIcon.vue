<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { channelIconRasterURL, namespacedChannelIconMarkup } from './channel-icons'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    icon?: string
    mark?: string
    name?: string
    groupName?: string
    size?: 'inherit' | 'sm' | 'md' | 'hero'
    surface?: boolean
    tooltip?: boolean
  }>(),
  {
    icon: undefined,
    mark: undefined,
    name: undefined,
    groupName: undefined,
    size: 'inherit',
    tooltip: true,
  },
)
const { t } = useI18n()
const id = `modern-channel-${useId()}`
const markup = computed(() => namespacedChannelIconMarkup(props.icon ?? '', id))
const raster = computed(() => channelIconRasterURL(props.icon ?? ''))
const fallback = computed(
  () =>
    props.mark?.trim() ||
    Array.from(props.name || props.icon || '?')
      .slice(0, 2)
      .join('')
      .toUpperCase(),
)
const tooltipLabel = computed(() => {
  const channelName = (props.name || props.mark || props.icon || '').trim()
  const groupName = props.groupName?.trim()
  return [
    channelName ? t('ui.channelTooltip.channel', { name: channelName }) : '',
    groupName ? t('ui.channelTooltip.group', { name: groupName }) : '',
  ]
    .filter(Boolean)
    .join('\n')
})
</script>

<template>
  <AppTooltip :label="tooltipLabel" :disabled="!tooltip">
    <span
      v-bind="$attrs"
      class="modern-channel-icon"
      :class="[`modern-channel-icon--${size}`, { 'has-surface': surface }]"
      aria-hidden="true"
    >
      <!-- 只渲染随构建发布的 SVG，接口只提供资源名，不能提供 HTML。 -->
      <!-- eslint-disable-next-line vue/no-v-html -->
      <span v-if="markup" class="modern-channel-icon-art" v-html="markup" />
      <img v-else-if="raster" :src="raster" alt="" />
      <span v-else class="modern-channel-icon-mark">{{ fallback }}</span>
    </span>
  </AppTooltip>
</template>

<style scoped>
.modern-channel-icon {
  --modern-channel-size: 1em;
  --modern-channel-glyph: 1em;
  display: inline-flex;
  width: var(--modern-channel-size);
  height: var(--modern-channel-size);
  flex: none;
  align-items: center;
  justify-content: center;
  vertical-align: middle;
}
.modern-channel-icon--sm {
  --modern-channel-size: var(--modern-channel-sm);
  --modern-channel-glyph: var(--modern-icon-md);
  font-size: var(--modern-icon-md);
}
.modern-channel-icon--md {
  --modern-channel-size: var(--modern-channel-md);
  --modern-channel-glyph: var(--modern-channel-sm);
  font-size: var(--modern-channel-sm);
}
.modern-channel-icon--hero {
  --modern-channel-size: var(--modern-channel-hero);
  --modern-channel-glyph: var(--modern-channel-hero-glyph);
  font-size: var(--modern-channel-hero-glyph);
}
.modern-channel-icon.has-surface {
  border-radius: var(--modern-radius-panel);
  background: var(--modern-subtle);
}
.modern-channel-icon-art,
.modern-channel-icon img {
  display: block;
  width: var(--modern-channel-glyph);
  height: var(--modern-channel-glyph);
  object-fit: contain;
}
.modern-channel-icon-art :deep(svg) {
  display: block;
  width: 100%;
  height: 100%;
}
.modern-channel-icon.has-surface .modern-channel-icon-mark {
  border: 0;
  background: transparent;
}
.modern-channel-icon-mark {
  display: grid;
  width: 100%;
  height: 100%;
  place-items: center;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  color: var(--modern-muted);
  font-size: var(--modern-channel-mark-ratio);
  font-weight: var(--modern-weight-semibold);
  line-height: var(--modern-leading-compact);
}
</style>
