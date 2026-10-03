import { computed, nextTick, onScopeDispose, ref, type CSSProperties, type Ref } from 'vue'

// 拖动只更新视觉位置，松手后才更新目录，避免拖动过程中反复请求容量预览。
export function useClientCatalogSort(options: {
  names: Ref<string[]>
  viewport: Ref<HTMLElement | undefined>
  list: Ref<HTMLElement | undefined>
  disabled: Ref<boolean>
  collapse: () => void
  move: (name: string, index: number) => void
  announce: (name: string, position: number) => void
}) {
  const name = ref('')
  const active = ref(false)
  const from = ref(0)
  const to = ref(0)
  const delta = ref(0)
  const height = ref(0)
  const placeholderTop = ref(0)
  let pointer = -1
  let handle: HTMLElement | undefined
  let startY = 0
  let currentY = 0
  let startScroll = 0
  let bounds: { top: number; height: number }[] = []
  let frame = 0
  let previousTime = 0
  let ready = false

  function clear(): void {
    const captured = pointer
    pointer = -1
    ready = false
    cancelAnimationFrame(frame)
    window.removeEventListener('pointermove', onMove)
    window.removeEventListener('pointerup', finish)
    window.removeEventListener('pointercancel', cancel)
    window.removeEventListener('blur', cancel)
    window.removeEventListener('keydown', onEscape, true)
    const previousHandle = handle
    handle = undefined
    if (captured >= 0 && previousHandle?.hasPointerCapture(captured))
      previousHandle.releasePointerCapture(captured)
    name.value = ''
    active.value = false
    delta.value = 0
  }
  function project(): void {
    if (!ready || !active.value) return
    const origin = bounds[from.value]
    const first = bounds[0]
    const last = bounds.at(-1)
    if (!origin || !first || !last) return
    const offset = currentY - startY + (options.viewport.value?.scrollTop ?? 0) - startScroll
    delta.value = Math.max(
      first.top - origin.top,
      Math.min(last.top + last.height - origin.top - origin.height, offset),
    )
    const center = origin.top + origin.height / 2 + delta.value
    let closest = from.value
    let distance = Infinity
    bounds.forEach((row, index) => {
      const candidate = Math.abs(center - row.top - row.height / 2)
      if (candidate < distance) {
        closest = index
        distance = candidate
      }
    })
    to.value = closest
    const target = bounds[closest]!
    placeholderTop.value =
      closest > from.value ? target.top + target.height - height.value : target.top
  }
  function scroll(time: number): void {
    if (pointer < 0) return
    const viewport = options.viewport.value
    if (active.value && viewport) {
      const rect = viewport.getBoundingClientRect()
      const edge = Math.min(48, rect.height / 4)
      const speed =
        currentY < rect.top + edge
          ? -Math.min(12, (rect.top + edge - currentY) / 4)
          : currentY > rect.bottom - edge
            ? Math.min(12, (currentY - rect.bottom + edge) / 4)
            : 0
      viewport.scrollTop += speed * Math.min(2, (time - previousTime) / 16 || 1)
      project()
    }
    previousTime = time
    frame = requestAnimationFrame(scroll)
  }
  function onMove(event: PointerEvent): void {
    if (event.pointerId !== pointer) return
    event.preventDefault()
    currentY = event.clientY
    if (Math.abs(currentY - startY) >= 4) active.value = true
    project()
  }
  function cancel(): void {
    clear()
  }
  function onEscape(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return
    event.preventDefault()
    event.stopImmediatePropagation()
    cancel()
  }
  function focus(model: string): void {
    void nextTick(() =>
      options.list.value
        ?.querySelector<HTMLElement>(`[data-model="${CSS.escape(model)}"] [data-sort-handle]`)
        ?.focus({ preventScroll: true }),
    )
  }
  function finish(event: PointerEvent): void {
    if (event.pointerId !== pointer) return
    const model = name.value
    const target = to.value
    const changed = active.value && from.value !== target
    clear()
    if (changed) {
      options.move(model, target)
      options.announce(model, target + 1)
    }
    focus(model)
  }
  async function start(event: PointerEvent, model: string): Promise<void> {
    if (options.disabled.value || event.button !== 0 || pointer >= 0) return
    event.preventDefault()
    pointer = event.pointerId
    handle = event.currentTarget as HTMLElement
    handle.focus({ preventScroll: true })
    handle.setPointerCapture(pointer)
    name.value = model
    from.value = options.names.value.indexOf(model)
    to.value = from.value
    startY = currentY = event.clientY
    const originalTop = handle.closest<HTMLElement>('[data-sort-row]')?.getBoundingClientRect().top
    options.collapse()
    window.addEventListener('pointermove', onMove, { passive: false })
    window.addEventListener('pointerup', finish)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('blur', cancel)
    window.addEventListener('keydown', onEscape, true)
    const captured = pointer
    await nextTick()
    if (pointer !== captured || !options.list.value || !options.viewport.value) return
    const listTop = options.list.value.getBoundingClientRect().top
    bounds = Array.from(options.list.value.querySelectorAll<HTMLElement>('[data-sort-row]')).map(
      (row) => {
        const rect = row.getBoundingClientRect()
        return { top: rect.top - listTop, height: rect.height }
      },
    )
    const origin = bounds[from.value]
    if (!origin) {
      clear()
      return
    }
    if (originalTop !== undefined) startY += listTop + origin.top - originalTop
    height.value = origin.height
    placeholderTop.value = origin.top
    startScroll = options.viewport.value.scrollTop
    ready = true
    project()
    previousTime = performance.now()
    frame = requestAnimationFrame(scroll)
  }
  function keyboard(event: KeyboardEvent, model: string): void {
    if (
      options.disabled.value ||
      pointer >= 0 ||
      !['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)
    )
      return
    event.preventDefault()
    const current = options.names.value.indexOf(model)
    const target =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? options.names.value.length - 1
          : current + (event.key === 'ArrowUp' ? -1 : 1)
    if (target < 0 || target >= options.names.value.length || target === current) return
    options.move(model, target)
    options.announce(model, target + 1)
    focus(model)
  }
  function style(index: number): CSSProperties | undefined {
    if (!active.value) return
    const offset =
      index === from.value
        ? delta.value
        : from.value < index && index <= to.value
          ? -height.value
          : to.value <= index && index < from.value
            ? height.value
            : 0
    return { transform: `translateY(${offset}px)` }
  }
  const placeholder = computed(() => ({
    top: `${placeholderTop.value}px`,
    height: `${height.value}px`,
  }))
  onScopeDispose(clear)
  return { name, active, start, keyboard, style, placeholder, cancel }
}
