<script setup lang="ts">
import AppTooltip from './AppTooltip.vue'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ text: string; fullText?: string }>()
const element = ref<HTMLElement>()
const overflow = ref(false)
let observer: ResizeObserver | undefined
function measure(): void {
  overflow.value = Boolean(element.value && element.value.scrollWidth > element.value.clientWidth)
}
const tooltip = computed(() =>
  overflow.value || (props.fullText && props.fullText !== props.text)
    ? (props.fullText ?? props.text)
    : undefined,
)
onMounted(() => {
  observer = new ResizeObserver(measure)
  if (element.value) observer.observe(element.value)
  measure()
})
watch(
  () => [props.text, props.fullText],
  () => void nextTick(measure),
)
onScopeDispose(() => observer?.disconnect())
</script>

<template>
  <AppTooltip :label="tooltip">
    <span ref="element" v-bind="$attrs" class="modern-overflow-text" @pointerenter="measure">{{
      text
    }}</span>
  </AppTooltip>
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
