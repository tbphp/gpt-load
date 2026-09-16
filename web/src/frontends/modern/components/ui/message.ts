export interface AppMessage {
  id: number
  text: string
  tone: 'info' | 'success' | 'warning' | 'danger'
  action?: { label: string; run: () => unknown }
}
