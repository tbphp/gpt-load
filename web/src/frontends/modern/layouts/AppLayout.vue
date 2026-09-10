<script setup lang="ts">
import { ChevronRight, Menu, PanelLeftClose, PanelLeftOpen, X } from '@lucide/vue'
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
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { findNavigationItem } from '@modern/app/navigation'
import { usePreferences } from '@modern/app/preferences'
import HintTooltip from '@modern/components/HintTooltip.vue'
import AppearanceMenu from './AppearanceMenu.vue'
import QuickNavigation from './QuickNavigation.vue'
import SidebarContent from './SidebarContent.vue'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const { sidebarCollapsed, toggleSidebar, persistenceFailed } = usePreferences()
const mobileOpen = ref(false)
const current = computed(() => findNavigationItem(route.name))
const pageTitle = computed(() =>
  current.value ? t(`pages.${current.value.id}.title`) : t('unavailableTitle'),
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
const removeAfterEach = router.afterEach((to, from) => {
  if (to.fullPath === from.fullPath) return
  requestAnimationFrame(() =>
    document.getElementById('modern-content')?.focus({ preventScroll: true }),
  )
})
onBeforeUnmount(removeAfterEach)
</script>

<template>
  <TooltipProvider :delay-duration="400">
    <a class="modern-skip-link" href="#modern-content">{{ t('skipToContent') }}</a>
    <div class="modern-app" :class="{ 'is-sidebar-collapsed': sidebarCollapsed }">
      <aside class="modern-sidebar"><SidebarContent :collapsed="sidebarCollapsed" /></aside>
      <div class="modern-main-column">
        <header class="modern-topbar">
          <HintTooltip
            :label="sidebarCollapsed ? t('shell.expandSidebar') : t('shell.collapseSidebar')"
          >
            <button
              class="modern-icon-button modern-desktop-toggle"
              type="button"
              :aria-label="sidebarCollapsed ? t('shell.expandSidebar') : t('shell.collapseSidebar')"
              @click="toggleSidebar"
            >
              <component
                :is="sidebarCollapsed ? PanelLeftOpen : PanelLeftClose"
                :size="18"
                :stroke-width="1.8"
                aria-hidden="true"
              />
            </button>
          </HintTooltip>
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
          <div class="modern-preview-notice">
            <span>{{ t('shell.preview') }}</span>
            <p>{{ t('shell.previewDescription') }}</p>
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
