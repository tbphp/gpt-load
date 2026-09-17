<script setup lang="ts">
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import {
  ChevronLeft,
  ChevronRight,
  CirclePlus,
  KeyRound,
  LogOut,
  Menu,
  RefreshCw,
  X,
} from '@lucide/vue'
import { DialogClose, DialogRoot, DialogTrigger } from 'reka-ui'
import { useIsFetching } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isNavigationFailure, RouterLink, useRoute, useRouter } from 'vue-router'
import { desktopMediaQuery } from '@modern/app/breakpoints'
import { pagePath } from '@modern/app/navigation'
import { usePreferences } from '@modern/app/preferences'
import { providePageRefresh } from '@modern/app/page-refresh'
import { useMessageSource } from '@modern/app/messages'
import { usePageTitle } from '@modern/app/use-page-title'
import {
  AppBadge,
  AppButton,
  AppDialogContent,
  AppIcon,
  AppIconButton,
  AppLoadingIndicator,
  AppTooltip,
} from '@modern/components/ui'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { provideSystemStatus } from '@modern/features/system/useSystemStatus'
import { loginLocation } from '@modern/app/redirect'
import AppearanceMenu from './AppearanceMenu.vue'
import { provideLoadingActivity, useLoadingFeedback } from '@modern/components/ui/loading'
import SidebarContent from './SidebarContent.vue'
import ImportGuide from '@modern/features/import/ImportGuide.vue'

const { t, locale } = useI18n()
const session = useAuthSession()
const route = useRoute()
const router = useRouter()
function closeImport(): void {
  const query = { ...route.query }
  delete query.import
  delete query.import_mode
  delete query.import_group
  void router.replace({ query })
}
const { sidebarCollapsed, toggleSidebar, persistenceFailed } = usePreferences()
provideSystemStatus()
const mobileOpen = ref(false)
const failedNavigation = ref<string | null>(null)
const loggingOut = ref(false)
const { title: pageTitle } = usePageTitle()
const pageRefresh = providePageRefresh()
const fetching = useIsFetching()
const localActivity = provideLoadingActivity()
const navigating = ref(false)
const loadingPage = ref(true)
let navigationTarget: string | undefined
const contentPending = computed(
  () => pageRefresh.busy.value || fetching.value > 0 || localActivity.value,
)
const pageLoading = useLoadingFeedback(
  () => navigating.value || loadingPage.value || pageRefresh.running.value,
)
const removeBeforeEach = router.beforeEach((to, from) => {
  navigationTarget = to.fullPath
  // query/hash 是页内状态，只有路径变化才作为页面导航。
  navigating.value = to.path !== from.path
})
watch(
  () => route.path,
  () => {
    loadingPage.value = true
  },
  { flush: 'sync' },
)
watch(
  [navigating, contentPending, () => route.path],
  async ([navigationPending, requestsPending]) => {
    if (navigationPending || requestsPending) return
    const path = route.path
    // 等待新页面挂载并登记请求；首轮加载完成后，局部请求不再点亮顶部进度条。
    await nextTick()
    if (route.path === path && !navigating.value && !contentPending.value) {
      loadingPage.value = false
    }
  },
  { immediate: true, flush: 'post' },
)
const refreshedAt = computed(() =>
  pageRefresh.updatedAt.value === undefined
    ? ''
    : t('shell.refreshedAt', {
        time: dateFormatter(locale.value, {
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
          hour12: false,
        }).format(pageRefresh.updatedAt.value),
      }),
)
watch(
  () => route.fullPath,
  () => {
    mobileOpen.value = false
  },
)
const removeAfterEach = router.afterEach((to, from, failure) => {
  if (navigationTarget === to.fullPath) navigating.value = false
  if (failure) return
  failedNavigation.value = null
  // 同页搜索和筛选只更新查询参数，保留当前控件的输入焦点。
  if (to.path === from.path) return
  requestAnimationFrame(() =>
    document.getElementById('modern-content')?.focus({ preventScroll: true }),
  )
})
const removeNavigationError = router.onError((_error, to) => {
  if (navigationTarget === to.fullPath) navigating.value = false
  failedNavigation.value = to.fullPath
})
let desktopMedia: MediaQueryList | undefined
function closeMobileOnDesktop(): void {
  if (desktopMedia?.matches) mobileOpen.value = false
}
function reloadFailedNavigation(): void {
  if (failedNavigation.value) window.location.assign(failedNavigation.value)
}
async function logout(): Promise<void> {
  if (loggingOut.value) return
  loggingOut.value = true
  try {
    const failure = await router.replace(loginLocation())
    // 路由守卫阻止离开时仍保留会话，后续业务页可继续使用未保存修改保护。
    if (!isNavigationFailure(failure)) session.clear()
  } catch {
    failedNavigation.value = pagePath('login')
  } finally {
    loggingOut.value = false
  }
}
onMounted(() => {
  desktopMedia = window.matchMedia(desktopMediaQuery)
  desktopMedia.addEventListener('change', closeMobileOnDesktop)
})
onBeforeUnmount(() => {
  removeBeforeEach()
  removeAfterEach()
  removeNavigationError()
  desktopMedia?.removeEventListener('change', closeMobileOnDesktop)
})
useMessageSource(() =>
  failedNavigation.value
    ? {
        text: t('shell.navigationFailed'),
        tone: 'danger',
        action: { label: t('shell.reload'), run: reloadFailedNavigation },
      }
    : undefined,
)
useMessageSource(() =>
  persistenceFailed.value
    ? { text: t('appearance.persistenceFailed'), tone: 'warning' }
    : undefined,
)
</script>

<template>
  <ImportGuide
    v-if="route.query.import === '1' && session.state.principalType === 'admin'"
    @close="closeImport"
  />
  <a class="modern-skip-link" href="#modern-content">{{ t('skipToContent') }}</a>
  <div class="modern-app" :class="{ 'is-sidebar-collapsed': sidebarCollapsed }">
    <aside id="modern-desktop-sidebar" class="modern-sidebar">
      <SidebarContent :collapsed="sidebarCollapsed" />
    </aside>
    <AppTooltip
      :label="sidebarCollapsed ? t('shell.expandSidebar') : t('shell.collapseSidebar')"
      side="right"
    >
      <button
        class="modern-sidebar-toggle"
        type="button"
        :aria-label="sidebarCollapsed ? t('shell.expandSidebar') : t('shell.collapseSidebar')"
        :aria-expanded="!sidebarCollapsed"
        aria-controls="modern-desktop-sidebar"
        @click="toggleSidebar"
      >
        <AppIcon :icon="sidebarCollapsed ? ChevronRight : ChevronLeft" size="xs" />
      </button>
    </AppTooltip>
    <div class="modern-main-column">
      <header class="modern-topbar">
        <h1>{{ pageTitle }}</h1>
        <div v-if="pageRefresh.available.value" class="modern-topbar-refresh">
          <span v-if="refreshedAt">{{ refreshedAt }}</span>
          <AppIconButton
            :icon="RefreshCw"
            :label="t('shell.refresh')"
            :loading="pageRefresh.pending.value"
            :disabled="pageRefresh.busy.value"
            @click="pageRefresh.run()"
          />
        </div>
        <span
          v-if="pageRefresh.available.value"
          class="modern-topbar-divider"
          aria-hidden="true"
        ></span>
        <div class="modern-toolbar">
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
          <AppTooltip
            v-if="session.state.principalType === 'access_key'"
            :label="t('auth.readOnlyDescription')"
          >
            <AppBadge class="modern-session-scope" :icon="KeyRound" tone="info">
              {{ t('auth.readOnly') }}
            </AppBadge>
          </AppTooltip>
          <AppTooltip
            v-if="session.state.principalType === 'admin'"
            :label="t('shell.importCredentials')"
          >
            <AppButton
              variant="brand"
              icon-only
              :aria-label="t('shell.importCredentials')"
              as-child
            >
              <RouterLink :to="{ path: route.path, query: { ...route.query, import: '1' } }"
                ><AppIcon :icon="CirclePlus" size="lg"
              /></RouterLink>
            </AppButton>
          </AppTooltip>
          <AppearanceMenu />
          <AppIconButton
            :icon="LogOut"
            :label="t('auth.logout')"
            :loading="loggingOut"
            @click="logout"
          />
        </div>
        <AppLoadingIndicator :loading="pageLoading" />
      </header>
      <main id="modern-content" class="modern-content" tabindex="-1">
        <slot />
      </main>
    </div>
  </div>
  <span class="modern-sr-only" role="status" aria-live="polite" aria-atomic="true">{{
    pageTitle
  }}</span>
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
  display: grid;
  grid-template-columns: var(--modern-sidebar-expanded) minmax(0, 1fr);
  height: 100dvh;
  overflow: hidden;
  transition: grid-template-columns var(--modern-motion-fast) var(--modern-motion-ease);
}
.modern-app.is-sidebar-collapsed {
  grid-template-columns: var(--modern-sidebar-collapsed) minmax(0, 1fr);
}
.modern-sidebar-toggle {
  position: fixed;
  z-index: var(--modern-layer-sidebar-toggle);
  top: 50dvh;
  left: var(--modern-sidebar-expanded);
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
  transition: left var(--modern-motion-fast) var(--modern-motion-ease);
}
.is-sidebar-collapsed .modern-sidebar-toggle {
  left: var(--modern-sidebar-collapsed);
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
  overscroll-behavior: contain;
  border-right: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-sidebar);
}
.modern-main-column {
  display: grid;
  height: 100%;
  min-height: 0;
  grid-template-rows: auto minmax(0, 1fr);
  min-width: 0;
}
/* 顶栏是所有页面共用的固定结构：标题 | 刷新与时间 | 竖线 | 全局控件。 */
.modern-topbar {
  position: sticky;
  z-index: var(--modern-layer-header);
  top: 0;
  display: flex;
  min-height: var(--modern-topbar-height);
  align-items: center;
  gap: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  background: var(--modern-surface);
  padding: 0 var(--modern-content-inset);
}
.modern-topbar h1 {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  color: var(--modern-text);
  font-size: var(--modern-font-size-title);
  font-weight: var(--modern-weight-semibold);
  letter-spacing: var(--modern-tracking-title);
  line-height: var(--modern-leading-title);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.modern-topbar-refresh {
  display: flex;
  min-width: 0;
  flex-shrink: 0;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
  white-space: nowrap;
}
.modern-topbar-divider {
  width: var(--modern-line-width);
  height: var(--modern-space-5);
  flex-shrink: 0;
  background: var(--modern-border);
}
.modern-toolbar {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-toolbar .modern-mobile-toggle {
  display: none;
}
.modern-session-scope {
  flex-shrink: 0;
  margin-right: var(--modern-space-2);
  white-space: nowrap;
}
.modern-content {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
  width: 100%;
  min-width: 0;
  padding: 0 var(--modern-content-inset) var(--modern-space-3);
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
@media (max-width: 760px) {
  .modern-app {
    display: block;
  }
  .modern-sidebar,
  .modern-sidebar-toggle {
    display: none;
  }
  /* 侧栏不可见时，抽屉入口回到标题行。 */
  .modern-toolbar .modern-mobile-toggle {
    display: inline-flex;
  }
  .modern-toolbar {
    gap: 0;
  }
  .modern-topbar {
    flex-wrap: wrap;
    gap: var(--modern-space-2);
    padding-block: var(--modern-space-2);
  }
  /* 移动端顶栏只留刷新按钮，时间文字和竖线让位给标题。 */
  .modern-topbar-refresh span,
  .modern-topbar-divider {
    display: none;
  }
  .modern-session-scope {
    order: 1;
    margin-right: 0;
  }
}
.modern-notices {
  display: grid;
  flex: none;
  gap: var(--modern-space-3);
  margin-bottom: var(--modern-space-5);
}
</style>
