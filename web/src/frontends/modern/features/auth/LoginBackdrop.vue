<script setup lang="ts">
import { onMounted, onScopeDispose, ref, watch } from 'vue'

import { usePreferences } from '@modern/app/preferences'
import { createLoginAurora } from './login-aurora'

const canvas = ref<HTMLCanvasElement>()
const { resolvedTheme } = usePreferences()
let aurora: ReturnType<typeof createLoginAurora>

onMounted(() => {
  if (canvas.value) aurora = createLoginAurora(canvas.value)
})
watch(resolvedTheme, () => aurora?.refreshColors(), { flush: 'post' })
onScopeDispose(() => aurora?.dispose())
</script>

<template>
  <div class="modern-login-backdrop" aria-hidden="true">
    <canvas ref="canvas" class="modern-login-backdrop__aurora" />
  </div>
</template>

<style scoped>
.modern-login-backdrop {
  position: absolute;
  z-index: var(--modern-layer-underlay);
  inset: 0;
  overflow: hidden;
  background:
    radial-gradient(
      ellipse at 12% 78%,
      color-mix(in srgb, var(--modern-login-aurora-primary) 12%, transparent),
      transparent 55%
    ),
    radial-gradient(
      ellipse at 88% 22%,
      color-mix(in srgb, var(--modern-login-aurora-secondary) 14%, transparent),
      transparent 55%
    ),
    var(--modern-login-canvas);
  /* Canvas 从计算后的颜色取值，明暗主题仍由全局 token 决定。 */
  color: var(--modern-login-aurora-secondary);
  pointer-events: none;
  user-select: none;
}
.modern-login-backdrop__aurora {
  display: block;
  width: 100%;
  height: 100%;
  color: var(--modern-login-aurora-primary);
  opacity: var(--modern-login-aurora-opacity);
  /* 光幕可以自由变形，表单周围的安静区域始终固定。 */
  mask-image: radial-gradient(ellipse 44% 52% at 50% 50%, transparent 22%, var(--modern-text) 100%);
}
</style>
