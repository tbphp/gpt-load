import { computed } from 'vue'

import { useAuthSession } from '@modern/features/auth/auth-session'
import { navigationItems } from './navigation'

export function useNavigation() {
  const session = useAuthSession()
  return computed(() =>
    navigationItems.filter(
      (item) =>
        session.state.phase === 'validated' &&
        (!item.adminOnly || session.state.principalType === 'admin'),
    ),
  )
}
