import { onScopeDispose, ref } from 'vue'
import { desktopMediaQuery } from './breakpoints'

// 仅用于手机端不同的交互结构；尺寸与排版继续由 CSS 控制。
export function useMobileLayout() {
  const media = window.matchMedia(desktopMediaQuery)
  const mobile = ref(!media.matches)
  const update = () => {
    mobile.value = !media.matches
  }
  media.addEventListener('change', update)
  onScopeDispose(() => media.removeEventListener('change', update))
  return mobile
}
