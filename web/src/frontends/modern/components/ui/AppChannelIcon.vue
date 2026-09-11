<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { computed, useId } from 'vue'
import { channelIconRasterURL, namespacedChannelIconMarkup } from './channel-icons'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ icon?: string; mark?: string; name?: string }>()
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
</script>

<template>
  <AppTooltip :label="name || mark || icon">
    <span v-bind="$attrs" class="modern-channel-icon" aria-hidden="true">
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
  display: inline-flex;
  width: 1em;
  height: 1em;
  flex: none;
  align-items: center;
  justify-content: center;
  vertical-align: middle;
}
.modern-channel-icon-art,
.modern-channel-icon-art :deep(svg),
.modern-channel-icon img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
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
