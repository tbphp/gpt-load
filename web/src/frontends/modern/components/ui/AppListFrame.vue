<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'
import { onMounted, onUpdated, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppIcon from './AppIcon.vue'
import { readListScroll, saveListScroll } from './list-scroll'

const props = defineProps<{
  label: string
  loading?: boolean
  scrollKey?: string
  flow?: boolean
}>()
const { t } = useI18n()
const scroller = ref<HTMLElement>()
// 遮罩和可交互状态跟随真实任务；防闪烁的延时只用于非阻塞的视觉提示。
let restoreTo = readListScroll(props.scrollKey)
function restoreScroll(): void {
  if (restoreTo === undefined || props.loading || !scroller.value) return
  scroller.value.scrollTop = restoreTo
  restoreTo = undefined
}
function onScroll(): void {
  if (restoreTo === undefined && scroller.value)
    saveListScroll(props.scrollKey, scroller.value.scrollTop)
}
onMounted(restoreScroll)
onUpdated(restoreScroll)
defineExpose({
  scrollToTop: () => {
    restoreTo = undefined
    scroller.value?.scrollTo({ top: 0 })
    saveListScroll(props.scrollKey, 0)
  },
})
</script>

<template>
  <section
    class="modern-list-frame"
    :class="{ 'modern-list-frame--flow': flow }"
    :aria-label="label"
  >
    <div class="modern-list-body">
      <div
        ref="scroller"
        class="modern-list-scroll"
        role="region"
        :aria-label="label"
        :aria-busy="loading || undefined"
        tabindex="0"
        @scroll="onScroll"
      >
        <div v-if="$slots.header" class="modern-list-header"><slot name="header" /></div>
        <slot />
      </div>
      <div v-if="loading" class="modern-list-loading" role="status">
        <span><AppIcon :icon="LoaderCircle" class="modern-spin" />{{ t('ui.loading') }}</span>
      </div>
    </div>
    <slot name="footer" />
  </section>
</template>

<style scoped>
.modern-list-frame {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  flex-direction: column;
  text-align: left;
}
/* 表头与内容同属一个滚动容器：横向滚动天然同步，纵向滚动时吸顶。
   宽度交给插槽内容自己决定，避免与数据行用不同算法而错位。 */
.modern-list-header {
  position: sticky;
  z-index: var(--modern-layer-raised);
  top: 0;
  background: var(--modern-surface);
}
.modern-list-body {
  position: relative;
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
}
.modern-list-scroll {
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  overscroll-behavior: contain;
}
.modern-list-scroll:focus-visible {
  outline-offset: calc(-1 * var(--modern-focus-width));
}
.modern-list-frame--flow {
  flex: none;
}
.modern-list-frame--flow .modern-list-body {
  display: block;
  flex: none;
}
.modern-list-frame--flow .modern-list-scroll {
  overflow: visible;
}
.modern-list-frame--flow .modern-list-header {
  position: static;
}
.modern-list-loading {
  position: absolute;
  z-index: var(--modern-layer-raised);
  inset: 0;
  display: grid;
  place-items: center;
  background: var(--modern-loading-overlay);
  cursor: progress;
}
.modern-list-loading > span {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  box-shadow: var(--modern-shadow-control);
}
</style>
