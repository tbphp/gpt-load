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
}

export interface FieldProps {
  label: string
  labelHidden?: boolean
  inline?: boolean | 'subgrid'
  id?: string
  description?: string
  descriptionWarning?: string
  error?: string
  invalid?: boolean
  describedBy?: string
  disabled?: boolean
}
