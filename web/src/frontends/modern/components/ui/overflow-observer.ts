// 溢出检测共用一个 ResizeObserver：表格一屏有数百个文本单元格，
// 每个各建一个观察者的开销远大于回调本身。
type Callback = () => void

let observer: ResizeObserver | undefined
const callbacks = new WeakMap<Element, Callback>()

export function observeOverflow(element: Element, callback: Callback): () => void {
  observer ??= new ResizeObserver((entries) => {
    for (const entry of entries) callbacks.get(entry.target)?.()
  })
  callbacks.set(element, callback)
  observer.observe(element)
  return () => {
    observer?.unobserve(element)
    callbacks.delete(element)
  }
}
