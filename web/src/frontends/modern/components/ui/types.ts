export type ControlSize = 'xs' | 'sm' | 'md'
export type ButtonVariant = 'default' | 'primary' | 'ghost' | 'brand' | 'danger' | 'text'
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
