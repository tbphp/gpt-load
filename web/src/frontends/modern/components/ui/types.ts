export type ControlSize = 'xs' | 'sm' | 'md'
// 按钮比通用控件多一档，用于卡片内不需要强调的次级操作。
export type ButtonSize = 'xxs' | ControlSize
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
}

export interface FieldProps {
  label: string
  labelHidden?: boolean
  id?: string
  description?: string
  error?: string
  invalid?: boolean
  describedBy?: string
  disabled?: boolean
}
