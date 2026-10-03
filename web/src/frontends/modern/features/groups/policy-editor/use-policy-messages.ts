import { useI18n } from 'vue-i18n'
import { policyEditorMessages } from '@modern/i18n/locales/policy-editor'

// 组件级本地文案：无需改动既有 locales 聚合，集成时可选择全局注册。
export function usePolicyMessages() {
  return useI18n({ messages: policyEditorMessages, useScope: 'local' })
}
