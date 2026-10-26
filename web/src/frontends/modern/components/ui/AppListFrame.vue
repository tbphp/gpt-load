<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppIcon from './AppIcon.vue'

const props = defineProps<{
  label: string
  loading?: boolean
  flow?: boolean
  mobileFlow?: boolean
}>()
const { t } = useI18n()
const scroller = ref<HTMLElement>()
defineExpose({
  scrollToTop: () => {
    const element = scroller.value
    element?.scrollTo({ top: 0 })
    // 手机列表随页面滚动，翻页后也要回到列表顶部。
    if (props.mobileFlow && element && element.scrollHeight <= element.clientHeight) {
      let parent = element.parentElement
      while (parent) {
        if (['auto', 'scroll'].includes(getComputedStyle(parent).overflowY)) {
          parent.scrollTo({ top: 0 })
          break
        }
        parent = parent.parentElement
      }
    }
  },
})
</script>

<template>
  <section
    class="modern-list-frame"
    :class="{ 'modern-list-frame--flow': flow, 'modern-list-frame--mobile-flow': mobileFlow }"
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
  background: var(--modern-list-header-background, var(--modern-surface));
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
  /* 数据行更新时不锚定旧行，避免新行被推到可视区域上方。 */
  overflow-anchor: none;
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
/* 手机页面随主内容滚动，避免筛选栏与分页挤掉列表；编辑浮层仍保留独立滚动。 */
@media (max-width: 760px) {
  .modern-list-frame--mobile-flow {
    flex: none;
  }
  .modern-list-frame--mobile-flow .modern-list-body {
    display: block;
    flex: none;
  }
  .modern-list-frame--mobile-flow .modern-list-scroll {
    overflow: visible;
  }
  .modern-list-frame--mobile-flow .modern-list-header {
    position: static;
  }
}
</style>
