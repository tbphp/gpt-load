<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, useId, watch } from 'vue'

const props = defineProps<{
  compact?: boolean
  resolvedTheme: 'light' | 'dark'
}>()
type Reaction = 'idle' | 'sniff' | 'nudge' | 'fold' | 'unfold' | 'theme'

const root = ref<HTMLElement>()
const body = ref<SVGGElement>()
const reaction = ref<Reaction>('idle')
const charging = ref(false)
const sequence = ref(0)
const look = ref({ x: 0, y: 0 })
const identity = useId()
const silhouette = 'mascot-shape-' + identity
const bodyClip = 'mascot-body-' + identity
const tailClip = 'mascot-tail-' + identity
const pose = computed(() => ({
  '--modern-mascot-look-x': look.value.x + 'px',
  '--modern-mascot-look-y': look.value.y + 'px',
}))
let reducedMotion: MediaQueryList | undefined
let hoverTimer: ReturnType<typeof setTimeout> | undefined
let holdTimer: ReturnType<typeof setTimeout> | undefined
let pointer: { id: number; x: number; y: number; consumed: boolean } | undefined
let lastSniff = -Infinity
let suppressClickUntil = 0
let disposed = false

function canAnimate(): boolean {
  return Boolean(
    !disposed &&
    reducedMotion &&
    !reducedMotion.matches &&
    !document.hidden &&
    root.value?.getClientRects().length,
  )
}
function timing(name: 'hover-delay' | 'charge-duration' | 'cooldown'): number {
  const value = getComputedStyle(root.value!)
    .getPropertyValue('--modern-mascot-' + name)
    .trim()
  return Number.parseFloat(value) * (value.endsWith('ms') ? 1 : 1000)
}
function clearHover(): void {
  clearTimeout(hoverTimer)
  hoverTimer = undefined
}
function cancelPress(): void {
  clearTimeout(holdTimer)
  holdTimer = undefined
  charging.value = false
  if (pointer?.consumed) suppressClickUntil = performance.now() + 800
  const id = pointer?.id
  pointer = undefined
  if (id !== undefined && root.value?.hasPointerCapture(id)) root.value.releasePointerCapture(id)
}
function reset(): void {
  clearHover()
  cancelPress()
  reaction.value = 'idle'
}
function play(next: Reaction): void {
  if (!canAnimate()) return
  clearHover()
  sequence.value++
  reaction.value = next
}
function nudge(): void {
  if (reaction.value === 'nudge' || !canAnimate()) return
  reset()
  play('nudge')
}
function nearMascot(event: PointerEvent): boolean {
  const rect = root.value?.getBoundingClientRect()
  if (!rect || !rect.width) return false
  const width = props.compact ? rect.width : rect.width * 0.32
  const x = event.clientX - rect.left
  const y = event.clientY - rect.top
  if (x < 0 || x > width || y < 0 || y > rect.height) return false
  look.value = {
    x: Math.round((x / width - 0.35) * 28),
    y: Math.round((y / rect.height - 0.5) * 20),
  }
  return true
}
function hover(event: PointerEvent): void {
  if (pointer) {
    if (Math.hypot(event.clientX - pointer.x, event.clientY - pointer.y) > 10) {
      cancelPress()
    }
    return
  }
  if (event.pointerType !== 'mouse' || !canAnimate()) return
  if (!nearMascot(event)) {
    clearHover()
    return
  }
  if (
    reaction.value !== 'idle' ||
    hoverTimer !== undefined ||
    performance.now() - lastSniff < timing('cooldown')
  )
    return
  hoverTimer = setTimeout(() => {
    hoverTimer = undefined
    if (reaction.value !== 'idle' || !canAnimate()) return
    lastSniff = performance.now()
    play('sniff')
  }, timing('hover-delay'))
}
function leave(): void {
  clearHover()
  if (pointer && !pointer.consumed) cancelPress()
}
function press(event: PointerEvent): void {
  if (event.isPrimary) suppressClickUntil = 0
  if (
    event.button !== 0 ||
    !event.isPrimary ||
    event.ctrlKey ||
    event.metaKey ||
    event.altKey ||
    event.shiftKey ||
    !canAnimate() ||
    !nearMascot(event)
  )
    return
  reset()
  suppressClickUntil = 0
  pointer = { id: event.pointerId, x: event.clientX, y: event.clientY, consumed: false }
  root.value?.setPointerCapture(event.pointerId)
  charging.value = true
  holdTimer = setTimeout(() => {
    holdTimer = undefined
    if (!pointer || !canAnimate()) return
    pointer.consumed = true
    charging.value = false
    play('nudge')
  }, timing('charge-duration'))
}
function release(): void {
  cancelPress()
}
function click(event: MouseEvent): void {
  // 仅吞掉长按后的那次指针点击；普通点击与 Enter 仍交给首页链接。
  const keyboardClick = event.detail === 0 && !('pointerType' in event && event.pointerType)
  if (keyboardClick || performance.now() >= suppressClickUntil) return
  suppressClickUntil = 0
  event.preventDefault()
  event.stopPropagation()
}
function contextMenu(event: MouseEvent): void {
  if (pointer) event.preventDefault()
}
function finish(event: AnimationEvent): void {
  if (event.target !== body.value || event.animationName.startsWith('modern-mascot-charge')) return
  reaction.value = 'idle'
}
function cancelWhenHidden(): void {
  if (document.hidden) reset()
}
function motionPreferenceChanged(): void {
  if (reducedMotion?.matches) reset()
}
watch(
  () => props.compact,
  (compact) => {
    reset()
    play(compact ? 'fold' : 'unfold')
  },
)
watch(
  () => props.resolvedTheme,
  () => {
    reset()
    // 仅响应最终生效模式的变化；首次渲染及同色的偏好切换不播放。
    play('theme')
  },
)
onMounted(() => {
  reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  reducedMotion.addEventListener('change', motionPreferenceChanged)
  document.addEventListener('visibilitychange', cancelWhenHidden)
})
onScopeDispose(() => {
  disposed = true
  reset()
  reducedMotion?.removeEventListener('change', motionPreferenceChanged)
  document.removeEventListener('visibilitychange', cancelWhenHidden)
})
defineExpose({ nudge })
</script>

<template>
  <span
    ref="root"
    class="modern-brand-mascot"
    :class="[
      'is-' + reaction,
      { 'is-compact': compact, 'is-charging': charging, 'is-dark': resolvedTheme === 'dark' },
    ]"
    :style="pose"
    @pointerenter="hover"
    @pointermove="hover"
    @pointerleave="leave"
    @pointerdown="press"
    @pointerup="release"
    @pointercancel="release"
    @lostpointercapture="release"
    @click.capture="click"
    @contextmenu="contextMenu"
    @animationend="finish"
  >
    <svg
      :key="sequence"
      class="modern-brand-mascot-scene"
      :viewBox="compact ? '0 0 512 512' : '0 0 1200 300'"
      :width="compact ? 32 : 192"
      :height="compact ? 32 : 48"
      role="img"
      aria-label="GPT-Load"
      focusable="false"
    >
      <defs>
        <!-- 沿用原 Logo 的轮廓和字标路径；裁切只用于分离尾巴。 -->
        <path
          :id="silhouette"
          fill-rule="evenodd"
          d="M 257.47 0.47 C 280.84 -0.99 304.38 0.1 327.65 2.35 C 346.64 4.19 366.32 6.86 382.88 17.12 C 407.97 32.65 425.34 58.28 451.47 72.53 C 469.41 82.31 489.13 86.89 508.24 93.76 C 517.44 97.08 527.63 100.44 535.41 106.59 C 545.92 114.88 548 128.45 548 141 C 548 155.44 546.7 171.18 535.94 181.94 C 526.87 191.01 510.4 193.38 498.47 196.47 C 485.06 199.95 471.76 203.88 458.35 207.35 C 450.09 209.5 439.94 211.04 432.59 215.59 C 423.59 221.16 420.23 234.99 425.88 244.12 C 429.61 250.13 436.95 250.74 443.06 252.94 C 455.36 257.37 465.69 270.34 462.12 284.12 C 458.79 296.97 443.26 295 433 295 C 409.67 295 386.33 295 363 295 C 353.26 295 340.17 297.21 331.12 292.88 C 324.26 289.6 321.96 282.55 322.47 275.35 C 323.23 264.7 329.4 254.24 326.59 243.41 C 317.96 210.15 289.31 193.37 256.94 189.06 C 220.4 184.19 177.47 189.7 156.53 223.53 C 152.9 229.4 148.6 237.81 148.29 244.82 C 148.03 250.84 154.62 253.15 158.24 256.76 C 165.74 264.26 171.43 278.72 164.06 288.06 C 157.78 296.01 141.76 293 133 293 C 107 293 81 293 55 293 C 45.74 293 32.14 295.38 24.88 288.12 C 16.35 279.59 21.19 267.1 22.94 256.94 C 25.76 240.6 30.44 224.6 34.59 208.59 C 36.14 202.62 41.47 193.01 39.24 186.94 C 37.61 182.53 30.3 180.81 26.71 178.29 C 9.06 165.94 0 146.4 0 125 C 0 117.31 0.74 107.31 6.53 101.53 C 11.47 96.59 18.08 99.67 21.24 104.76 C 28.41 116.36 33.31 126.71 46.71 132.29 C 50.98 134.08 56.91 136.36 61.29 133.82 C 66.56 130.78 69.15 120.37 72.29 115.29 C 81.45 100.5 90.79 86.26 101.59 72.59 C 125.83 41.89 160.6 17.44 198.59 7.59 C 217.75 2.62 237.8 1.7 257.47 0.47 Z M 458.88 136.12 C 461.21 145.98 471.28 152.37 481.24 150.24 C 491.44 148.05 497.45 138.08 495.06 127.94 C 492.68 117.9 483.1 111.68 472.94 113.94 C 462.9 116.17 456.5 126.06 458.88 136.12 Z"
        />
        <clipPath :id="bodyClip">
          <path d="M72 0V115L38 187L0 211V310H560V0Z" />
        </clipPath>
        <clipPath :id="tailClip">
          <path d="M0 0H74V116L40 188L0 212Z" />
        </clipPath>
      </defs>
      <rect v-if="compact" class="modern-brand-mascot-tile" width="512" height="512" rx="128" />
      <g
        class="modern-brand-mascot-figure"
        :transform="
          compact ? 'translate(51.2 145.7518) scale(0.7474)' : 'translate(56 75.7117) scale(0.5036)'
        "
      >
        <g ref="body" class="modern-brand-mascot-body">
          <g class="modern-brand-mascot-tail">
            <use :href="'#' + silhouette" :clip-path="'url(#' + tailClip + ')'" />
          </g>
          <use :href="'#' + silhouette" :clip-path="'url(#' + bodyClip + ')'" />
          <ellipse class="modern-brand-mascot-lid" cx="477" cy="132" rx="23" ry="23" />
          <path class="modern-brand-mascot-closed-eye" d="M462 135Q477 146 492 133" />
        </g>
        <g class="modern-brand-mascot-scent" aria-hidden="true">
          <path d="M569 116Q581 128 569 140M590 107Q607 128 590 149" />
        </g>
        <g class="modern-brand-mascot-impact" aria-hidden="true">
          <path d="M635 89L654 68M646 116L675 112M641 143L660 158" />
        </g>
        <g class="modern-brand-mascot-day" aria-hidden="true">
          <circle cx="389" cy="-25" r="13" />
          <path
            d="M389 -56V-49M389 -1V6M358 -25H365M413 -25H420M367 -47L372 -42M406 -8L411 -3M367 -3L372 -8M406 -42L411 -47"
          />
        </g>
        <g class="modern-brand-mascot-night" aria-hidden="true">
          <path d="M412 -51A30 30 0 1 0 437 -4A27 27 0 0 1 412 -51Z" />
          <path d="M460 -48V-32M452 -40H468" />
        </g>
      </g>
      <g v-if="!compact" transform="translate(388 88.0957) scale(1.0957)">
        <g class="modern-brand-mascot-wordmark">
          <path
            fill-rule="evenodd"
            d="M 48.44 0.44 C 61.32 -1.17 78 -0.55 89.33 6.67 C 93.24 9.15 99.55 12.04 101.33 16.67 C 103.77 23.01 94.58 31.54 89.67 34.67 C 83.87 38.35 76.15 29.29 70.78 27.22 C 56.52 21.74 37.67 28.67 32.11 43.11 C 24.34 63.31 36.97 85.7 59 87 C 63.65 87.27 68.75 85.63 73 84 C 74.93 83.26 76.84 81.73 78.44 80.44 C 79.73 79.42 80.9 78.15 81.78 76.78 C 82.77 75.21 84.86 73.19 84.44 71.11 C 83.92 68.48 79.86 69 78 69 C 73.47 69 64.46 70.68 60.89 67.11 C 58.77 64.99 59 61.76 59 59 C 59 55.98 58.28 51.7 60 49 C 63.3 43.81 74.82 46 80 46 C 87.67 46 95.33 46 103 46 C 105.86 46 109.97 45.52 112.22 47.78 C 115.24 50.79 115 55.05 115 59 C 115 67.73 112.55 76.9 108.67 84.67 C 95.32 111.36 59.35 119.11 33.33 107.67 C -0.7 92.69 -11.97 44.41 15.22 17.22 C 19.76 12.68 25.29 8.86 31 6 C 36.32 3.34 42.52 1.19 48.44 0.44 Z M 128.67 2.67 C 135.15 0.17 145.1 2 152 2 C 169.36 2 189.34 -0.21 202.78 13.22 C 204.98 15.43 206.64 18.04 208.44 20.56 C 212.46 26.17 214 34.24 214 41 C 214 61.7 201.39 78.07 180.44 81.44 C 174.38 82.42 168.12 82 162 82 C 159.55 82 155.84 81.29 153.67 82.67 C 151.34 84.15 152 87.68 152 90 C 152 95.65 154.17 105.99 148.56 109.56 C 143.94 112.49 136.93 110.74 131.89 110.11 C 129.74 109.84 127.51 109.74 125.89 108.11 C 123.49 105.71 124 101.05 124 98 C 124 74 124 50 124 26 C 124 19.74 121.12 5.57 128.67 2.67 Z M 666.67 2.67 C 671.17 0.69 679.16 1.72 684 2 C 685.71 2.1 687.89 2.54 688.89 4.11 C 690.27 6.28 690 9.55 690 12 C 690 17 690 22 690 27 C 690 46 690 65 690 84 C 690 89.67 690 95.33 690 101 C 690 103.23 690.17 106.72 688.44 108.44 C 685.17 111.72 673.52 110.56 669.11 110.22 C 665.64 109.96 665.07 105 662.33 105 C 659.67 105 655.63 108.99 653 110 C 645.18 113.01 635.79 112.74 627.89 110.11 C 600.18 100.87 594.91 59.98 614.44 40.44 C 623.04 31.85 634.74 28.84 646.67 30.33 C 650.65 30.83 655.03 32.38 658.44 34.56 C 659.24 35.06 661.25 37.53 662.33 36.44 C 663.78 34.99 663 30.84 663 29 C 663 22.5 662.31 15.55 663.11 9.11 C 663.44 6.5 664.02 3.83 666.67 2.67 Z M 467.44 30.44 C 489.36 26.91 514.76 38.89 518.56 62.44 C 522.34 85.91 508.52 107.69 484.56 111.56 C 462.45 115.12 438.08 102.74 433.44 79.56 C 428.78 56.22 444.13 34.21 467.44 30.44 Z M 550.44 30.44 C 555.7 29.79 561.62 29.45 566.89 30.11 C 570.73 30.59 574.49 31.5 578.11 32.89 C 601.1 41.73 597 69.19 597 89 C 597 94.7 599.48 107.8 592.22 110.22 C 589.63 111.09 586.63 110 584 110 C 581.77 110 579.42 110.22 577.22 109.78 C 574.6 109.25 573.6 105.22 571.56 105.22 C 569.22 105.22 565.24 108.98 562.89 109.89 C 553.21 113.61 541.04 112.5 532.56 106.44 C 521.25 98.37 519.29 79.16 529.22 69.22 C 534.23 64.21 542.02 61.41 549 61 C 553.8 60.72 559.56 60.45 564.22 61.78 C 565.67 62.19 568.92 63.86 570.22 62.56 C 574.43 58.35 565.1 51.89 562 51 C 556.91 49.55 551.04 50.22 546.11 52.11 C 543.14 53.25 540 55.62 536.67 54.33 C 531.99 52.53 526.81 41.96 530.89 37.89 C 535.24 33.54 544.58 31.18 550.44 30.44 Z M 221.67 2.67 C 227.15 0.56 235.19 2 241 2 C 254.67 2 268.33 2 282 2 C 288.28 2 297.53 0.28 303.44 2.56 C 308.33 4.44 308 10.71 308 15 C 308 17.39 308.1 19.95 307.22 22.22 C 304.51 29.28 292.84 27 287 27 C 284.34 27 279.37 25.98 277.67 28.67 C 275.48 32.11 277 40.01 277 44 C 277 57.67 277 71.33 277 85 C 277 91.29 279.07 102.03 275.56 107.56 C 274.4 109.36 271.98 109.88 270 110 C 265.37 110.27 253.46 112.59 250.33 107.67 C 248.1 104.16 249 98.95 249 95 C 249 85.67 249 76.33 249 67 C 249 57.33 249 47.67 249 38 C 249 35.18 250.17 29.47 247.33 27.67 C 245.57 26.54 242.98 27 241 27 C 236.33 27 231.67 27 227 27 C 224.77 27 221.28 27.17 219.56 25.44 C 215.17 21.06 214.94 5.25 221.67 2.67 Z M 360.67 2.67 C 365.88 0.66 380.2 0.16 383.56 5.44 C 386.59 10.21 385 18.62 385 24 C 385 39.33 385 54.67 385 70 C 385 73.56 383.7 80.25 385.67 83.33 C 387.56 86.3 394.03 85 397 85 C 404 85 411 85 418 85 C 421.4 85 425.53 84.52 428.56 86.44 C 434.98 90.54 433.56 107.78 426.22 110.22 C 420.64 112.08 414.36 110.71 408.67 110.33 C 403.56 109.99 398.16 111 393 111 C 388.16 111 383.12 110.01 378.33 110.33 C 372.46 110.73 363.41 113.07 358.67 108.33 C 355.34 105.01 357 96.25 357 92 C 357 69.33 357 46.67 357 24 C 357 18.75 354.41 5.07 360.67 2.67 Z M 492.33 75.33 C 494.36 66.94 489.81 56.16 480.67 54.33 C 471.73 52.55 461.4 56.68 459.44 66.44 C 457.69 75.22 461.72 85.74 471.33 87.67 C 480.95 89.59 489.96 85.15 492.33 75.33 Z M 185.44 44.44 C 187.19 35.71 180.34 27.88 171.89 27.11 C 167.32 26.7 162.59 27 158 27 C 156.11 27 153.83 26.83 152.67 28.67 C 151.05 31.21 152 36.1 152 39 C 152 42.79 150.06 54.04 153.67 56.33 C 156.58 58.18 162.66 57 166 57 C 175.59 57 183.35 54.93 185.44 44.44 Z M 662.33 76.33 C 664.91 67.33 660.37 57.53 651.44 54.56 C 643.16 51.79 633.71 56.42 630.89 64.89 C 628.04 73.44 631.36 84.67 640.67 87.33 C 649.68 89.91 659.65 85.71 662.33 76.33 Z M 301.67 59.67 C 304.69 58.34 308.79 59 312 59 C 319.33 59 326.67 59 334 59 C 336.41 59 340.17 58.98 341.67 61.33 C 344.34 65.53 344.6 77.66 338.44 79 C 334.29 79.9 329.26 79 325 79 C 319.53 79 313.86 79.92 308.44 79.56 C 305.9 79.39 301.58 79.25 299.67 77.33 C 296.25 73.91 296.9 61.77 301.67 59.67 Z M 571.33 86.33 C 572.69 80.01 567.54 76.62 561.89 76.11 C 556.01 75.58 549.11 77.57 548.22 84.22 C 547.4 90.41 552.25 94.46 558.11 94.67 C 563.7 94.86 570.06 92.26 571.33 86.33 Z"
          />
        </g>
      </g>
    </svg>
  </span>
</template>

<style scoped>
.modern-brand-mascot {
  --modern-mascot-look-x: 0px;
  --modern-mascot-look-y: 0px;
  --modern-mascot-eye-color: var(--modern-mascot-backdrop);
  display: block;
  flex: none;
  width: var(--modern-logo-width);
  max-width: 100%;
  aspect-ratio: 4;
  color: var(--modern-action);
  user-select: none;
  -webkit-touch-callout: none;
}
.modern-brand-mascot.is-compact {
  --modern-mascot-eye-color: var(--modern-action);
  width: var(--modern-control-sm);
  aspect-ratio: 1;
}
.modern-brand-mascot-scene {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
}
.modern-brand-mascot-figure {
  fill: currentColor;
}
.is-compact .modern-brand-mascot-figure {
  color: var(--modern-mascot-inverse);
}
.modern-brand-mascot-tile {
  fill: var(--modern-action);
  transform-origin: 256px 256px;
}
.modern-brand-mascot-body {
  transform-origin: 270px 275px;
}
.modern-brand-mascot-tail {
  transform-origin: 57px 162px;
}
.modern-brand-mascot-lid {
  transform-origin: 477px 132px;
  transform: scaleY(0);
}
.modern-brand-mascot-closed-eye {
  fill: none;
  stroke: var(--modern-mascot-eye-color);
  stroke-width: var(--modern-mascot-stroke);
  opacity: 0;
}
.modern-brand-mascot-wordmark {
  fill: var(--modern-text);
  transform-origin: 0 82px;
}
.modern-brand-mascot-scent,
.modern-brand-mascot-impact,
.modern-brand-mascot-day,
.modern-brand-mascot-night {
  fill: none;
  stroke: currentColor;
  stroke-width: var(--modern-mascot-stroke);
  stroke-linecap: round;
  stroke-linejoin: round;
  opacity: 0;
  pointer-events: none;
}
.modern-brand-mascot-night {
  display: none;
}
.is-charging .modern-brand-mascot-body {
  animation: modern-mascot-charge var(--modern-mascot-charge-duration) var(--modern-mascot-ease)
    both;
}
.is-sniff .modern-brand-mascot-body {
  animation: modern-mascot-sniff var(--modern-mascot-sniff-duration) var(--modern-mascot-ease) both;
}
.is-sniff .modern-brand-mascot-tail,
.is-unfold .modern-brand-mascot-tail {
  animation: modern-mascot-wag var(--modern-mascot-sniff-duration) var(--modern-mascot-ease) both;
}
.is-sniff .modern-brand-mascot-lid,
.is-unfold .modern-brand-mascot-lid {
  animation: modern-mascot-blink var(--modern-mascot-sniff-duration) linear both;
}
.is-sniff .modern-brand-mascot-scent {
  animation: modern-mascot-scent var(--modern-mascot-sniff-duration) var(--modern-mascot-ease) both;
}
.is-nudge .modern-brand-mascot-body {
  animation: modern-mascot-nudge var(--modern-mascot-nudge-duration) var(--modern-mascot-ease) both;
}
.is-nudge .modern-brand-mascot-wordmark {
  animation: modern-mascot-wordmark var(--modern-mascot-nudge-duration) var(--modern-mascot-ease)
    both;
}
.is-nudge .modern-brand-mascot-impact {
  animation: modern-mascot-impact var(--modern-mascot-nudge-duration) linear both;
}
.is-compact.is-nudge .modern-brand-mascot-impact {
  display: none;
}
.is-compact.is-nudge .modern-brand-mascot-tile {
  animation: modern-mascot-pocket var(--modern-mascot-nudge-duration) var(--modern-mascot-ease) both;
}
.is-fold .modern-brand-mascot-body {
  animation: modern-mascot-fold var(--modern-mascot-fold-duration) var(--modern-mascot-ease) both;
}
.is-fold .modern-brand-mascot-tile {
  animation: modern-mascot-pocket var(--modern-mascot-fold-duration) var(--modern-mascot-ease) both;
}
.is-unfold .modern-brand-mascot-body {
  animation: modern-mascot-unfold var(--modern-mascot-sniff-duration) var(--modern-mascot-ease) both;
}
.is-unfold .modern-brand-mascot-wordmark {
  animation: modern-mascot-reveal var(--modern-mascot-sniff-duration) var(--modern-mascot-ease) both;
}
.is-theme .modern-brand-mascot-body {
  animation: modern-mascot-wake var(--modern-mascot-theme-duration) var(--modern-mascot-ease) both;
}
.is-theme .modern-brand-mascot-lid {
  animation: modern-mascot-day-eyes var(--modern-mascot-theme-duration) linear both;
}
.is-theme .modern-brand-mascot-day,
.is-theme .modern-brand-mascot-night {
  animation: modern-mascot-mood var(--modern-mascot-theme-duration) var(--modern-mascot-ease) both;
}
.modern-brand-mascot.is-dark.is-theme .modern-brand-mascot-body {
  animation-name: modern-mascot-doze;
}
.modern-brand-mascot.is-dark.is-theme .modern-brand-mascot-lid {
  animation-name: modern-mascot-night-eyes;
}
.modern-brand-mascot.is-dark.is-theme .modern-brand-mascot-closed-eye {
  animation: modern-mascot-sleep-eye var(--modern-mascot-theme-duration) linear both;
}
.modern-brand-mascot.is-dark .modern-brand-mascot-day {
  display: none;
}
.modern-brand-mascot.is-dark .modern-brand-mascot-night {
  display: block;
}
@keyframes modern-mascot-charge {
  from {
    transform: none;
  }
  to {
    transform: translateX(-12px) scale(1.05, 0.84);
  }
}
@keyframes modern-mascot-sniff {
  0%,
  100% {
    transform: none;
  }
  25%,
  52% {
    transform: translate(var(--modern-mascot-look-x), var(--modern-mascot-look-y)) rotate(-2deg)
      scale(1.04, 0.98);
  }
  38%,
  64% {
    transform: translate(calc(var(--modern-mascot-look-x) + 12px), var(--modern-mascot-look-y))
      rotate(-3deg) scale(1.055, 0.97);
  }
  82% {
    transform: translateY(-5px);
  }
}
@keyframes modern-mascot-wag {
  0%,
  18%,
  100% {
    transform: none;
  }
  30%,
  52% {
    transform: rotate(-17deg);
  }
  41%,
  63% {
    transform: rotate(12deg);
  }
  78% {
    transform: rotate(-5deg);
  }
}
@keyframes modern-mascot-blink {
  0%,
  65%,
  76%,
  100% {
    transform: scaleY(0);
  }
  70% {
    transform: scaleY(1);
  }
}
@keyframes modern-mascot-scent {
  0%,
  24%,
  70%,
  100% {
    opacity: 0;
    transform: translateX(6px);
  }
  32%,
  52% {
    opacity: var(--modern-opacity-quiet);
    transform: translateX(0);
  }
}
@keyframes modern-mascot-nudge {
  0% {
    transform: translateX(-12px) scale(1.05, 0.84);
  }
  18% {
    transform: translateX(-24px) rotate(3deg) scale(1.07, 0.88);
  }
  36% {
    transform: translate(90px, -8px) rotate(-4deg) scale(1.05, 1.02);
  }
  45% {
    transform: translate(76px, -6px) scale(0.98, 1.05);
  }
  65% {
    transform: translate(-7px, -10px) rotate(2deg);
  }
  83% {
    transform: translateY(4px) scale(1.015, 0.98);
  }
  100% {
    transform: none;
  }
}
@keyframes modern-mascot-wordmark {
  0%,
  33% {
    transform: none;
  }
  39% {
    transform: translate(24px, -8px) rotate(-1.5deg);
  }
  52% {
    transform: translate(-5px, 2px) rotate(0.6deg);
  }
  70% {
    transform: translate(2px, -1px) rotate(-0.2deg);
  }
  100% {
    transform: none;
  }
}
@keyframes modern-mascot-impact {
  0%,
  33%,
  52%,
  100% {
    opacity: 0;
  }
  37%,
  43% {
    opacity: var(--modern-opacity-quiet);
  }
}
@keyframes modern-mascot-fold {
  0% {
    transform: translate(16px, -25px) scale(1.32, 1.12);
  }
  30% {
    transform: translateY(25px) rotate(5deg) scale(0.75, 0.62);
  }
  60% {
    transform: translateY(-12px) rotate(-3deg) scale(1.07, 1.08);
  }
  80% {
    transform: translateY(4px) scale(1.015, 0.97);
  }
  100% {
    transform: none;
  }
}
@keyframes modern-mascot-pocket {
  0% {
    transform: scale(0.82);
  }
  40% {
    transform: scale(1.045);
  }
  70% {
    transform: scale(0.99);
  }
  100% {
    transform: none;
  }
}
@keyframes modern-mascot-unfold {
  0% {
    transform: translateY(15px) scale(0.65, 0.74);
  }
  28% {
    transform: translateY(-16px) scale(1.08, 1.14);
  }
  46% {
    transform: translateY(4px) scale(1.025, 0.96);
  }
  68% {
    transform: translateY(-4px) scale(0.99, 1.025);
  }
  100% {
    transform: none;
  }
}
@keyframes modern-mascot-reveal {
  0%,
  9% {
    opacity: 0;
    transform: translateX(-25px);
  }
  42% {
    opacity: 1;
    transform: translateX(4px);
  }
  65%,
  100% {
    opacity: 1;
    transform: none;
  }
}
@keyframes modern-mascot-wake {
  0%,
  100% {
    transform: none;
  }
  18% {
    transform: translateY(9px) scale(1.05, 0.85);
  }
  45% {
    transform: translateY(-19px) rotate(-3deg) scale(0.96, 1.13);
  }
  63% {
    transform: translateY(4px) rotate(1deg) scale(1.025, 0.97);
  }
  80% {
    transform: translateY(-3px);
  }
}
@keyframes modern-mascot-doze {
  0%,
  100% {
    transform: none;
  }
  27%,
  66% {
    transform: translateY(12px) rotate(2deg) scale(1.04, 0.87);
  }
  78% {
    transform: translateY(-4px) scale(0.99, 1.035);
  }
}
@keyframes modern-mascot-day-eyes {
  0%,
  29% {
    transform: scaleY(1);
  }
  42%,
  58%,
  74%,
  100% {
    transform: scaleY(0);
  }
  66% {
    transform: scaleY(1);
  }
}
@keyframes modern-mascot-night-eyes {
  0%,
  12%,
  84%,
  100% {
    transform: scaleY(0);
  }
  32%,
  67% {
    transform: scaleY(1);
  }
}
@keyframes modern-mascot-sleep-eye {
  0%,
  24%,
  78%,
  100% {
    opacity: 0;
  }
  34%,
  64% {
    opacity: 1;
  }
}
@keyframes modern-mascot-mood {
  0%,
  14%,
  92%,
  100% {
    opacity: 0;
    transform: translateY(12px);
  }
  32%,
  67% {
    opacity: var(--modern-opacity-quiet);
    transform: translateY(0);
  }
}
</style>
