import type { SearchSelectOption } from './ui/types'

// 渠道别名统一来自渠道目录；匹配和键盘选择由 AppSearchSelect 处理。
export function channelSearchOption(channel: {
  id: string
  name: string
  keywords?: readonly string[]
}): SearchSelectOption {
  return { value: channel.id, label: channel.name, keywords: channel.keywords ?? [] }
}
