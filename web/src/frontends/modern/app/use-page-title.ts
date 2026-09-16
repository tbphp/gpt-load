import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { findNavigationItem } from './navigation'

// 文档标题、面包屑和路由播报共用同一份标题推导，避免多处各写一遍回退顺序。
export function usePageTitle() {
  const route = useRoute()
  const { t } = useI18n()
  const current = computed(() => findNavigationItem(route.meta.primaryNav ?? route.name))
  const title = computed(() =>
    route.meta.titleKey
      ? t(route.meta.titleKey)
      : current.value
        ? t(`pages.${current.value.id}.title`)
        : t('notFound.title'),
  )
  return { current, title }
}
