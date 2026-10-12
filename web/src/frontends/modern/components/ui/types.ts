export type ControlSize = 'xxs' | 'xs' | 'sm' | 'md'
// 紧凑行内编辑的输入框和按钮共用尺寸。
export type ButtonSize = ControlSize
export type ButtonVariant =
  'default' | 'primary' | 'outline' | 'ghost' | 'brand' | 'danger' | 'text'
export type SemanticTone = 'neutral' | 'info' | 'success' | 'warning' | 'danger'

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
}

export interface SearchSelectOption extends SelectOption {
  description?: string
  keywords?: readonly string[]
  // 未输入关键词时按相邻的同名 group 分组显示；输入关键词后按 searchOrder 回到自然顺序，
  // 置顶分组不会让搜索结果加权。
  group?: string
  searchOrder?: number
  class?: string
}

export interface FieldProps {
  label: string
  labelHidden?: boolean
  inline?: boolean | 'subgrid'
  id?: string
  hint?: string
  description?: string
  descriptionWarning?: string
  error?: string
  invalid?: boolean
  describedBy?: string
  disabled?: boolean
}
