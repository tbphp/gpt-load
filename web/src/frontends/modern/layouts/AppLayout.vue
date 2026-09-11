<script setup lang="ts">
import { ChevronLeft, ChevronRight, Menu, X } from '@lucide/vue'
import { DialogClose, DialogRoot, DialogTrigger, TooltipProvider } from 'reka-ui'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { desktopMediaQuery } from '@modern/app/breakpoints'
import { findNavigationItem } from '@modern/app/navigation'
import { usePreferences } from '@modern/app/preferences'
import AppButton from '@modern/components/ui/AppButton.vue'
import AppDialogContent from '@modern/components/ui/AppDialogContent.vue'
import AppIcon from '@modern/components/ui/AppIcon.vue'
import AppIconButton from '@modern/components/ui/AppIconButton.vue'
import AppNotice from '@modern/components/ui/AppNotice.vue'
import { tooltipDelay } from '@modern/components/ui/overlay'
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
  desktopMedia = window.matchMedia(desktopMediaQuery)
  desktopMedia.addEventListener('change', closeMobileOnDesktop)
})
onBeforeUnmount(() => {
  removeAfterEach()
  removeNavigationError()
  desktopMedia?.removeEventListener('change', closeMobileOnDesktop)
})
</script>

<template>
  <TooltipProvider :delay-duration="tooltipDelay">
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
        <AppIcon :icon="sidebarCollapsed ? ChevronRight : ChevronLeft" size="xs" />
      </button>
      <div class="modern-main-column">
        <header class="modern-topbar">
          <DialogRoot v-model:open="mobileOpen">
            <DialogTrigger as-child>
              <AppIconButton class="modern-mobile-toggle" :icon="Menu" :label="t('navigation')" />
            </DialogTrigger>
            <AppDialogContent
              placement="sidebar"
              :title="t('navigation')"
              :description="t('shell.mobileNavigationDescription')"
            >
              <DialogClose as-child>
                <AppIconButton class="modern-mobile-close" :icon="X" :label="t('shell.close')" />
              </DialogClose>
              <SidebarContent @navigate="mobileOpen = false" />
            </AppDialogContent>
          </DialogRoot>
          <div class="modern-breadcrumb" aria-hidden="true">
            <span>{{ current ? t(`sections.${current.section}`) : t('sections.workspace') }}</span
            ><AppIcon :icon="ChevronRight" size="xs" /><strong>{{ pageTitle }}</strong>
          </div>
          <div class="modern-topbar-actions">
            <QuickNavigation /><span class="modern-toolbar-divider" aria-hidden="true"></span
            ><AppearanceMenu />
          </div>
        </header>
        <main id="modern-content" class="modern-content" tabindex="-1">
          <div v-if="failedNavigation || persistenceFailed" class="modern-notices">
            <AppNotice v-if="failedNavigation" tone="danger" bordered>
              {{ t('shell.navigationFailed') }}
              <template #actions>
                <AppButton @click="reloadFailedNavigation">{{ t('shell.reload') }}</AppButton>
              </template>
            </AppNotice>
            <AppNotice v-if="persistenceFailed" tone="warning">
              {{ t('appearance.persistenceFailed') }}
            </AppNotice>
          </div>
          <slot />
        </main>
      </div>
    </div>
    <span class="modern-sr-only" role="status" aria-live="polite" aria-atomic="true">{{
      pageTitle
    }}</span>
  </TooltipProvider>
</template>

<style scoped>
.modern-skip-link {
  position: fixed;
  z-index: var(--modern-layer-skip-link);
  top: var(--modern-space-2);
  left: var(--modern-space-2);
  transform: translateY(-200%);
  border: var(--modern-line-width) solid var(--modern-accent);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  padding: var(--modern-space-2) var(--modern-space-3);
}
.modern-skip-link:focus {
  transform: none;
}
.modern-app {
  --modern-sidebar-width: var(--modern-sidebar-expanded);
  display: grid;
  grid-template-columns: var(--modern-sidebar-width) minmax(0, 1fr);
  min-height: 100dvh;
}
.modern-app.is-sidebar-collapsed {
  --modern-sidebar-width: var(--modern-sidebar-collapsed);
}
.modern-sidebar-toggle {
  position: fixed;
  z-index: var(--modern-layer-sidebar-toggle);
  top: 50dvh;
  left: var(--modern-sidebar-width);
  display: grid;
  width: var(--modern-space-6);
  height: var(--modern-touch-target);
  place-items: center;
  border: 0;
  border-radius: var(--modern-radius-control);
  background: transparent;
  padding: 0;
  color: var(--modern-muted);
  transform: translate(-50%, -50%);
}
.modern-sidebar-toggle::before {
  position: absolute;
  inset: var(--modern-space-1-5) var(--modern-space-1);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  content: '';
}
.modern-sidebar-toggle > svg {
  position: relative;
  opacity: var(--modern-opacity-quiet);
}
.modern-sidebar-toggle:hover > svg,
.modern-sidebar-toggle:focus-visible > svg {
  color: var(--modern-accent);
  opacity: 1;
}
.modern-sidebar {
  position: sticky;
  top: 0;
  height: 100dvh;
  overflow-y: auto;
  border-right: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-sidebar);
}
.modern-main-column {
  min-width: 0;
}
.modern-topbar {
  position: sticky;
  z-index: var(--modern-layer-header);
  top: 0;
  display: flex;
  height: var(--modern-topbar-height);
  align-items: center;
  gap: var(--modern-space-4);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  padding: 0 var(--modern-content-inset);
}
.modern-topbar .modern-mobile-toggle {
  display: none;
}
.modern-breadcrumb {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-text-small);
}
.modern-breadcrumb strong {
  overflow: hidden;
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-breadcrumb > span {
  white-space: nowrap;
}
.modern-topbar-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  margin-left: auto;
}
.modern-toolbar-divider {
  width: var(--modern-line-width);
  height: var(--modern-space-5);
  margin: 0 var(--modern-space-2);
  background: var(--modern-border);
}
.modern-content {
  width: 100%;
  min-width: 0;
  padding: var(--modern-content-top) var(--modern-content-inset) var(--modern-content-bottom);
}
.modern-content:focus {
  outline: none;
}
.modern-mobile-close {
  position: absolute;
  z-index: var(--modern-layer-raised);
  top: var(--modern-space-3);
  right: var(--modern-space-2);
}
@media (max-width: 1150px) {
  .modern-breadcrumb > span,
  .modern-breadcrumb > svg {
    display: none;
  }
}
@media (max-width: 760px) {
  .modern-app {
    display: block;
  }
  .modern-sidebar,
  .modern-sidebar-toggle {
    display: none;
  }
  .modern-topbar .modern-mobile-toggle {
    display: inline-flex;
  }
  .modern-topbar {
    height: var(--modern-topbar-height);
    gap: var(--modern-space-2);
    padding: 0 var(--modern-content-inset);
  }
  .modern-topbar-actions {
    gap: 0;
  }
  .modern-toolbar-divider {
    display: none;
  }
}
.modern-notices {
  display: grid;
  gap: var(--modern-space-3);
  margin-bottom: var(--modern-space-5);
}
</style>
