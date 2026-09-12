<script setup lang="ts">
import { LoaderCircle } from '@lucide/vue'
import { nextTick, onMounted, onScopeDispose, onUpdated, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppIcon from './AppIcon.vue'
import { useLoadingFeedback } from './loading'
import { readListScroll, saveListScroll } from './list-scroll'

const props = defineProps<{
  label: string
  loading?: boolean
  scrollKey?: string
  flow?: boolean
}>()
const { t } = useI18n()
const scroller = ref<HTMLElement>()
const header = ref<HTMLElement>()
const scrollbarWidth = ref(0)
const busy = useLoadingFeedback(() => Boolean(props.loading))
let observer: ResizeObserver | undefined
let restoreTo = readListScroll(props.scrollKey)
function restoreScroll(): void {
  if (restoreTo === undefined || props.loading || !scroller.value) return
  scroller.value.scrollTop = restoreTo
  restoreTo = undefined
}
function onScroll(): void {
  synchronize()
  if (restoreTo === undefined && scroller.value)
    saveListScroll(props.scrollKey, scroller.value.scrollTop)
}
function synchronize(): void {
  if (!scroller.value) return
  scrollbarWidth.value = scroller.value.offsetWidth - scroller.value.clientWidth
  if (header.value) header.value.scrollLeft = scroller.value.scrollLeft
}
onMounted(() => {
  observer = new ResizeObserver(synchronize)
  if (scroller.value) observer.observe(scroller.value)
  synchronize()
  restoreScroll()
})
onUpdated(restoreScroll)
onScopeDispose(() => observer?.disconnect())
watch(scrollbarWidth, () => void nextTick(synchronize))
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
    <div
      v-if="$slots.header"
      class="modern-list-header"
      :style="{ paddingRight: scrollbarWidth + 'px' }"
    >
      <div ref="header" class="modern-list-header-track"><slot name="header" /></div>
    </div>
    <div class="modern-list-body">
      <div
        ref="scroller"
        class="modern-list-scroll"
        role="region"
        :aria-label="label"
        :aria-busy="busy || undefined"
        tabindex="0"
        @scroll="onScroll"
      >
        <slot />
      </div>
      <div v-if="busy" class="modern-list-loading" role="status">
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
.modern-list-header {
  min-width: 0;
  flex: none;
}
.modern-list-header-track {
  overflow: hidden;
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
.modern-list-frame--flow .modern-list-scroll,
.modern-list-frame--flow .modern-list-header-track {
  overflow: visible;
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
