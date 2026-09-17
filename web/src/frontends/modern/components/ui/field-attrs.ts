import type { StyleValue } from 'vue'
// 布局样式作用于完整字段；输入事件和原生属性仍交给实际控件。
export function controlAttrs(attrs: Record<string, unknown>): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(attrs).filter(([key]) => key !== 'class' && key !== 'style'),
  )
}

export function layoutAttrs(attrs: Record<string, unknown>) {
  return { class: attrs.class, style: attrs.style as StyleValue | undefined }
}
