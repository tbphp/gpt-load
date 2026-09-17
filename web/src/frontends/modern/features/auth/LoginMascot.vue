<script setup lang="ts">
import { onMounted, onScopeDispose, ref } from 'vue'

import mascotBody from '@modern/assets/brand/login-mascot-body.webp'
import mascotEye from '@modern/assets/brand/login-mascot-eye.webp'
import mascotTail from '@modern/assets/brand/login-mascot-tail.webp'

defineProps<{ quiet?: boolean }>()

const root = ref<HTMLElement>()
const eye = ref<HTMLElement>()
const tail = ref<HTMLElement>()
const ready = ref(false)
const paused = ref(true)
const motion = {
  '--modern-login-mascot-tempo': 0.86 + Math.random() * 0.28,
  '--modern-login-mascot-phase': `${-Math.random() * 4}s`,
}
const loadedParts = new Set<'body' | 'eye' | 'tail'>()
let reducedMotion: MediaQueryList | undefined
let finePointer: MediaQueryList | undefined
let resizeObserver: ResizeObserver | undefined
let intersectionObserver: IntersectionObserver | undefined
let bounds: DOMRect | undefined
let frame: number | undefined
let tracking = false
let inViewport = false
let disposed = false
let hasPointer = false
let pointerX = 0
let pointerY = 0
let lastEyeTransform = ''
let lastTailTransform = ''

function applyLook(x: number, y: number, angle: number): void {
  if (!eye.value || !tail.value) return
  const eyeTransform = `translate(${x.toFixed(3)}px, ${y.toFixed(3)}px)`
  const tailTransform = `rotate(${angle.toFixed(3)}deg)`
  // 只写两处局部 transform，不让高频坐标沿根节点的 CSS 变量向整棵子树继承。
  if (eyeTransform !== lastEyeTransform) {
    eye.value.style.transform = eyeTransform
    lastEyeTransform = eyeTransform
  }
  if (tailTransform !== lastTailTransform) {
    tail.value.style.transform = tailTransform
    lastTailTransform = tailTransform
  }
}

function updateLook(): void {
  frame = undefined
  if (!tracking || disposed || !root.value) return
  if (!hasPointer) {
    applyLook(0, 0, 0)
    return
  }
  // 尺寸、滚动或卡片高度变化时才重新测量；同一帧先读布局，再写样式。
  bounds ??= root.value.getBoundingClientRect()
  const x = Math.tanh((pointerX - bounds.left - bounds.width * 0.82) / 240)
  const y = Math.tanh((pointerY - bounds.top - bounds.height * 0.54) / 200)
  applyLook(x * bounds.width * 0.012, y * bounds.width * 0.009, x * 3)
}

function scheduleLook(): void {
  if (tracking && !disposed && frame === undefined) frame = requestAnimationFrame(updateLook)
}

function invalidateBounds(): void {
  bounds = undefined
  if (hasPointer) scheduleLook()
}

function followPointer(event: PointerEvent): void {
  if (event.pointerType !== 'mouse') return
  if (hasPointer && event.clientX === pointerX && event.clientY === pointerY) return
  hasPointer = true
  pointerX = event.clientX
  pointerY = event.clientY
  // 合并高频事件，只消费下一次绘制前的最新坐标；鼠标停下后不再申请帧。
  scheduleLook()
}

function rest(): void {
  if (!hasPointer) return
  hasPointer = false
  scheduleLook()
}

function setTracking(enabled: boolean): void {
  if (tracking === enabled) return
  tracking = enabled
  bounds = undefined
  if (enabled) {
    window.addEventListener('pointermove', followPointer, { passive: true })
    window.addEventListener('blur', rest)
    window.addEventListener('resize', invalidateBounds, { passive: true })
    window.addEventListener('scroll', invalidateBounds, { passive: true, capture: true })
    document.documentElement.addEventListener('pointerleave', rest)
  } else {
    window.removeEventListener('pointermove', followPointer)
    window.removeEventListener('blur', rest)
    window.removeEventListener('resize', invalidateBounds)
    window.removeEventListener('scroll', invalidateBounds, true)
    document.documentElement.removeEventListener('pointerleave', rest)
    if (frame !== undefined) cancelAnimationFrame(frame)
    frame = undefined
    hasPointer = false
    applyLook(0, 0, 0)
  }
}

function syncMotion(): void {
  if (disposed) return
  paused.value = !ready.value || !inViewport || document.hidden || Boolean(reducedMotion?.matches)
  setTracking(!paused.value && Boolean(finePointer?.matches))
}

function imageLoaded(part: 'body' | 'eye' | 'tail'): void {
  if (disposed) return
  loadedParts.add(part)
  if (loadedParts.size !== 3) return
  ready.value = true
  syncMotion()
}

onMounted(() => {
  const element = root.value
  if (!element) return
  reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  finePointer = window.matchMedia('(any-hover: hover) and (any-pointer: fine)')
  resizeObserver = new ResizeObserver(invalidateBounds)
  resizeObserver.observe(element)
  // 登录提示、帮助展开和文字换行会改变卡片高度，进而改变居中后的吉祥物位置。
  if (element.parentElement) resizeObserver.observe(element.parentElement)
  intersectionObserver = new IntersectionObserver(([entry]) => {
    if (!entry) return
    inViewport = entry.isIntersecting
    syncMotion()
  })
  intersectionObserver.observe(element)
  document.addEventListener('visibilitychange', syncMotion)
  reducedMotion.addEventListener('change', syncMotion)
  finePointer.addEventListener('change', syncMotion)
  syncMotion()
})

onScopeDispose(() => {
  disposed = true
  setTracking(false)
  resizeObserver?.disconnect()
  intersectionObserver?.disconnect()
  document.removeEventListener('visibilitychange', syncMotion)
  reducedMotion?.removeEventListener('change', syncMotion)
  finePointer?.removeEventListener('change', syncMotion)
})
</script>

<template>
  <div
    ref="root"
    class="modern-login-mascot"
    :class="{ 'is-ready': ready, 'is-paused': paused, 'is-quiet': quiet }"
    :style="motion"
    aria-hidden="true"
  >
    <div class="modern-login-mascot__rig">
      <div ref="tail" class="modern-login-mascot__tail">
        <span class="modern-login-mascot__sprite modern-login-mascot__tail-art">
          <img
            :src="mascotTail"
            alt=""
            draggable="false"
            decoding="async"
            @load="imageLoaded('tail')"
          />
        </span>
      </div>
      <span class="modern-login-mascot__sprite modern-login-mascot__body">
        <img
          :src="mascotBody"
          alt=""
          draggable="false"
          decoding="async"
          @load="imageLoaded('body')"
        />
      </span>
      <div ref="eye" class="modern-login-mascot__eye">
        <span class="modern-login-mascot__sprite modern-login-mascot__eye-art">
          <img
            :src="mascotEye"
            alt=""
            draggable="false"
            decoding="async"
            @load="imageLoaded('eye')"
          />
        </span>
      </div>
    </div>
    <span class="modern-login-mascot__rim" />
    <span class="modern-login-mascot__sprite modern-login-mascot__rear-paw">
      <img :src="mascotBody" alt="" draggable="false" decoding="async" />
    </span>
    <span class="modern-login-mascot__sprite modern-login-mascot__front-paw">
      <img :src="mascotBody" alt="" draggable="false" decoding="async" />
    </span>
  </div>
</template>

<style scoped>
.modern-login-mascot {
  --modern-login-mascot-tempo: 1;
  --modern-login-mascot-phase: 0s;
  position: absolute;
  z-index: var(--modern-layer-raised);
  top: 0;
  left: calc(var(--modern-space-8) - var(--modern-login-mascot-width) * 0.12);
  width: var(--modern-login-mascot-width);
  aspect-ratio: 1200 / 620;
  pointer-events: none;
  user-select: none;
  opacity: 0;
  transition: opacity var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-login-mascot.is-ready {
  opacity: 1;
}
/* 遮挡只从卡片边框内侧开始；顶部线条由真实的爪子轮廓遮住。 */
.modern-login-mascot__rim {
  position: absolute;
  top: calc(
    var(--modern-login-mascot-width) * var(--modern-login-mascot-seat) + var(--modern-line-width)
  );
  bottom: 0;
  left: 0;
  width: 100%;
  background: var(--modern-login-card-surface);
}
.modern-login-mascot__rim::after {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(
      ellipse 10% 16% at 32% 34%,
      var(--modern-login-mascot-shadow),
      transparent 100%
    ),
    radial-gradient(
      ellipse 11% 16% at 65% 47%,
      var(--modern-login-mascot-shadow),
      transparent 100%
    ),
    radial-gradient(
      ellipse 13% 26% at 33% 36%,
      var(--modern-login-mascot-shadow-soft),
      transparent 100%
    ),
    radial-gradient(
      ellipse 14% 26% at 66% 49%,
      var(--modern-login-mascot-shadow-soft),
      transparent 100%
    );
  content: '';
}
.modern-login-mascot__rig {
  position: absolute;
  inset: 0;
  transform-origin: 60% 84.1935%;
  animation: modern-login-mascot-breathe
    calc(var(--modern-login-mascot-breath-duration) * var(--modern-login-mascot-tempo)) ease-in-out
    var(--modern-login-mascot-phase) infinite;
}
.modern-login-mascot__sprite {
  --modern-login-sprite-x: 0;
  --modern-login-sprite-y: 0;
  --modern-login-sprite-image-width: 1040;
  position: absolute;
  display: block;
  overflow: hidden;
  contain: paint;
}
/* 无损素材只保留实际使用的区域；爪尖复用身体图，裁切比例与原画一致。 */
.modern-login-mascot__sprite img {
  position: absolute;
  left: calc(-100% * var(--modern-login-sprite-x) / var(--modern-login-sprite-width));
  top: calc(-100% * var(--modern-login-sprite-y) / var(--modern-login-sprite-height));
  display: block;
  width: calc(100% * var(--modern-login-sprite-image-width) / var(--modern-login-sprite-width));
  max-width: none;
  height: auto;
}
.modern-login-mascot__body {
  --modern-login-sprite-width: 1040;
  --modern-login-sprite-height: 560;
  left: 10.8333%;
  top: 4.5161%;
  width: 86.6667%;
  aspect-ratio: 1040 / 560;
}
/* 卡片先遮住远侧脚，再叠回贴在正面的爪尖；支撑点保持固定。 */
.modern-login-mascot__rear-paw {
  --modern-login-sprite-x: 144;
  --modern-login-sprite-y: 494;
  --modern-login-sprite-width: 216;
  --modern-login-sprite-height: 54;
  left: 22.8333%;
  top: calc(var(--modern-login-mascot-width) * var(--modern-login-mascot-seat));
  width: 18%;
  aspect-ratio: 216 / 54;
}
.modern-login-mascot__front-paw {
  --modern-login-sprite-x: 544;
  --modern-login-sprite-y: 494;
  --modern-login-sprite-width: 232;
  --modern-login-sprite-height: 66;
  left: 56.1667%;
  top: calc(var(--modern-login-mascot-width) * var(--modern-login-mascot-seat));
  width: 19.3333%;
  aspect-ratio: 232 / 66;
}
.modern-login-mascot__tail {
  position: absolute;
  left: 7.5%;
  top: 44.8387%;
  width: 15%;
  aspect-ratio: 272 / 240;
  transform-origin: 88% 76%;
  transform: rotate(0deg);
}
.modern-login-mascot__tail-art {
  --modern-login-sprite-image-width: 272;
  --modern-login-sprite-width: 272;
  --modern-login-sprite-height: 240;
  inset: 0;
  transform-origin: 88% 76%;
  animation: modern-login-mascot-tail
    calc(var(--modern-login-mascot-tail-duration) * var(--modern-login-mascot-tempo)) ease-in-out
    var(--modern-login-mascot-phase) infinite;
}
.modern-login-mascot__eye {
  position: absolute;
  left: 78%;
  top: 45.4839%;
  width: 8.5%;
  aspect-ratio: 1;
  transform: translate(0, 0);
}
.modern-login-mascot__eye-art {
  --modern-login-sprite-image-width: 160;
  --modern-login-sprite-width: 160;
  --modern-login-sprite-height: 160;
  inset: 0;
  animation: modern-login-mascot-blink
    calc(var(--modern-login-mascot-blink-duration) * var(--modern-login-mascot-tempo)) ease-in-out
    infinite;
}
.modern-login-mascot.is-quiet .modern-login-mascot__rig,
.modern-login-mascot.is-quiet .modern-login-mascot__tail-art,
.modern-login-mascot.is-paused .modern-login-mascot__rig,
.modern-login-mascot.is-paused .modern-login-mascot__tail-art,
.modern-login-mascot.is-paused .modern-login-mascot__eye-art {
  animation-play-state: paused;
}
@keyframes modern-login-mascot-breathe {
  0%,
  100% {
    transform: scaleY(1);
  }
  48% {
    transform: scaleY(1.01);
  }
}
@keyframes modern-login-mascot-tail {
  0%,
  100% {
    transform: rotate(-6deg);
  }
  28% {
    transform: rotate(9deg);
  }
  52% {
    transform: rotate(2deg);
  }
  74% {
    transform: rotate(7deg);
  }
}
@keyframes modern-login-mascot-blink {
  0%,
  94%,
  100% {
    transform: scaleY(1);
  }
  96%,
  97% {
    transform: scaleY(0.08);
  }
}
@media (max-width: 760px) {
  .modern-login-mascot {
    left: calc(var(--modern-space-6) - var(--modern-login-mascot-width) * 0.12);
  }
}
</style>
