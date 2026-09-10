<script setup lang="ts">
import { ChevronLeft, ChevronRight, Menu, X } from '@lucide/vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
  TooltipProvider,
} from 'reka-ui'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { findNavigationItem } from '@modern/app/navigation'
import { usePreferences } from '@modern/app/preferences'
import { provideSystemStatus } from '@modern/features/system/useSystemStatus'
import AppearanceMenu from './AppearanceMenu.vue'
import QuickNavigation from './QuickNavigation.vue'
import SidebarContent from './SidebarContent.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const { sidebarCollapsed, toggleSidebar, persistenceFailed } = usePreferences()
provideSystemStatus()
const mobileOpen = ref(false)
const failedNavigation = ref<string | null>(null)
const current = computed(() => findNavigationItem(route.meta.primaryNav ?? route.name))
const pageTitle = computed(() =>
  typeof route.meta.titleKey === 'string'
    ? t(route.meta.titleKey)
    : current.value
      ? t(`pages.${current.value.id}.title`)
      : t('notFound.title'),
)
watch(
  [pageTitle, locale],
  () => {
    document.title = `${pageTitle.value} · GPT-Load`
  },
  { immediate: true },
)
watch(
  () => route.fullPath,
  () => {
    mobileOpen.value = false
  },
)
const removeAfterEach = router.afterEach((to, from, failure) => {
  if (failure) return
  failedNavigation.value = null
  if (to.fullPath === from.fullPath) return
  requestAnimationFrame(() =>
    document.getElementById('modern-content')?.focus({ preventScroll: true }),
  )
})
const removeNavigationError = router.onError((_error, to) => {
  failedNavigation.value = to.fullPath
})
let desktopMedia: MediaQueryList | undefined
function closeMobileOnDesktop(): void {
  if (desktopMedia?.matches) mobileOpen.value = false
}
function reloadFailedNavigation(): void {
  if (failedNavigation.value) window.location.assign(failedNavigation.value)
}
onMounted(() => {
  desktopMedia = window.matchMedia('(min-width: 761px)')
  desktopMedia.addEventListener('change', closeMobileOnDesktop)
})
onBeforeUnmount(() => {
  removeAfterEach()
  removeNavigationError()
  desktopMedia?.removeEventListener('change', closeMobileOnDesktop)
})
</script>

<template>
  <TooltipProvider :delay-duration="400">
    <a class="modern-skip-link" href="#modern-content">{{ t('skipToContent') }}</a>
    <div class="modern-app" :class="{ 'is-sidebar-collapsed': sidebarCollapsed }">
      <aside id="modern-desktop-sidebar" class="modern-sidebar">
        <SidebarContent :collapsed="sidebarCollapsed" />
      </aside>
      <button
        class="modern-sidebar-toggle"
        type="button"
        :aria-label="sidebarCollapsed ? t('shell.expandSidebar') : t('shell.collapseSidebar')"
        :title="sidebarCollapsed ? t('shell.expandSidebar') : t('shell.collapseSidebar')"
        :aria-expanded="!sidebarCollapsed"
        aria-controls="modern-desktop-sidebar"
        @click="toggleSidebar"
      >
        <component
          :is="sidebarCollapsed ? ChevronRight : ChevronLeft"
          :size="12"
          :stroke-width="1.6"
          aria-hidden="true"
        />
      </button>
      <div class="modern-main-column">
        <header class="modern-topbar">
          <DialogRoot v-model:open="mobileOpen">
            <DialogTrigger
              class="modern-icon-button modern-mobile-toggle"
              :aria-label="t('navigation')"
              ><Menu :size="19" aria-hidden="true"
            /></DialogTrigger>
            <DialogPortal>
              <DialogOverlay class="modern-overlay" />
              <DialogContent class="modern-mobile-sidebar">
                <DialogTitle class="modern-sr-only">{{ t('navigation') }}</DialogTitle>
                <DialogDescription class="modern-sr-only">{{
                  t('shell.mobileNavigationDescription')
                }}</DialogDescription>
                <DialogClose
                  class="modern-icon-button modern-mobile-close"
                  :aria-label="t('shell.close')"
                  ><X :size="18" aria-hidden="true"
                /></DialogClose>
                <SidebarContent @navigate="mobileOpen = false" />
              </DialogContent>
            </DialogPortal>
          </DialogRoot>
          <div class="modern-breadcrumb" aria-hidden="true">
            <span>{{ current ? t(`sections.${current.section}`) : t('sections.workspace') }}</span
            ><ChevronRight :size="13" aria-hidden="true" /><strong>{{ pageTitle }}</strong>
          </div>
          <div class="modern-topbar-actions">
            <QuickNavigation /><span class="modern-toolbar-divider" aria-hidden="true"></span
            ><AppearanceMenu />
          </div>
        </header>
        <main id="modern-content" class="modern-content" tabindex="-1">
          <div v-if="failedNavigation" class="modern-navigation-error" role="alert">
            <span>{{ t('shell.navigationFailed') }}</span>
            <button type="button" class="modern-button" @click="reloadFailedNavigation">
              {{ t('shell.reload') }}
            </button>
          </div>
          <p v-if="persistenceFailed" class="modern-preference-notice" role="status">
            {{ t('appearance.persistenceFailed') }}
          </p>
          <slot />
        </main>
      </div>
    </div>
    <span class="modern-sr-only" role="status" aria-live="polite" aria-atomic="true">{{
      pageTitle
    }}</span>
  </TooltipProvider>
</template>
