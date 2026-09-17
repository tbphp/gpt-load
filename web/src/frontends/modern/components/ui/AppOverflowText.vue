<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { observeOverflow } from './overflow-observer'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ text: string; fullText?: string; hint?: string }>()
const element = ref<HTMLElement>()
const overflow = ref(false)
let unobserve: (() => void) | undefined
function measure(): void {
  overflow.value = Boolean(element.value && element.value.scrollWidth > element.value.clientWidth)
}
// 默认只在截断时提示；显式 hint 用于补充角色等未在正文显示的信息。
const tooltip = computed(() => (overflow.value ? (props.fullText ?? props.text) : props.hint))
watch(
  element,
  (node) => {
    unobserve?.()
    unobserve = undefined
    if (!node) return
    unobserve = observeOverflow(node, measure)
    measure()
  },
  { immediate: true, flush: 'post' },
)
watch(
  () => [props.text, props.fullText],
  () => void nextTick(measure),
)
onScopeDispose(() => unobserve?.())
</script>

<template>
  <AppTooltip v-if="tooltip" :label="tooltip">
    <span ref="element" v-bind="$attrs" class="modern-overflow-text" @pointerenter="measure">{{
      text
    }}</span>
  </AppTooltip>
  <span v-else ref="element" v-bind="$attrs" class="modern-overflow-text" @pointerenter="measure">{{
    text
  }}</span>
</template>

<style scoped>
.modern-overflow-text {
  display: block;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
}
</style>
