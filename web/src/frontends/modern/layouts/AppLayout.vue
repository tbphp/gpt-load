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
  Timer,
} from '@lucide/vue'
import { DialogRoot, DialogTrigger } from 'reka-ui'
import { useIsFetching, useQuery } from '@tanstack/vue-query'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isNavigationFailure, RouterLink, useRoute, useRouter } from 'vue-router'
import { desktopMediaQuery } from '@modern/app/breakpoints'
import { pagePath } from '@modern/app/navigation'
import { usePreferences } from '@modern/app/preferences'
import { providePageRefresh } from '@modern/app/page-refresh'
import { autoRefreshIntervals } from '@shared/auto-refresh'
import { useMessageSource } from '@modern/app/messages'
import { usePageTitle } from '@modern/app/use-page-title'
import {
  AppBadge,
  AppButton,
  AppDialogContent,
  AppDialogHeader,
  AppIcon,
  AppIconButton,
  AppLoadingIndicator,
  AppNotice,
  AppSelectMenu,
  AppTooltip,
} from '@modern/components/ui'
import { useAuthSession } from '@modern/features/auth/auth-session'
import { provideSystemStatus } from '@modern/features/system/useSystemStatus'
import { loginLocation } from '@modern/app/redirect'
import AppearanceMenu from './AppearanceMenu.vue'
import { provideLoadingActivity, useLoadingFeedback } from '@modern/components/ui/loading'
import SidebarContent from './SidebarContent.vue'
import MobileNavigation from './MobileNavigation.vue'
import { useMobileLayout } from '@modern/app/use-mobile-layout'
import { useNavigation } from '@modern/app/use-navigation'

const { t, locale } = useI18n()
const session = useAuthSession()
const route = useRoute()
const router = useRouter()
const createGroupLocation = computed(() => ({
  name: 'modern-groups',
  query: { ...(route.name === 'modern-groups' ? route.query : {}), panel: 'create' },
}))
const {
  sidebarCollapsed,
  toggleSidebar,
  persistenceFailed,
  autoRefreshInterval,
  setAutoRefreshInterval,
} = usePreferences()
const accessKeySession = computed(() => session.state.principalType === 'access_key')
// 常驻提示，不提供关闭入口。
const requestBodyRecording = computed(() =>
  accessKeySession.value ? session.state.requestBodyRecording : null,
)
// 管理员可能在登录后切换记录开关：标签页重新可见、切换页面和手动刷新时都重读会话。
const recordingRefresh = useQuery({
  queryKey: ['modern', 'auth', 'request-body-recording'],
  queryFn: ({ signal }) => session.refreshRequestBodyRecording(signal),
  enabled: accessKeySession,
})
function refreshRecordingNotice(): void {
  if (accessKeySession.value) void recordingRefresh.refetch()
}
watch(() => route.path, refreshRecordingNotice)
function refreshPage(): void {
  refreshRecordingNotice()
  void pageRefresh.run()
}
provideSystemStatus()
const mobileNavigation = ref<InstanceType<typeof MobileNavigation>>()
const mobileOpen = ref(false)
const isMobile = useMobileLayout()
const navigation = useNavigation()
const moreNavigation = computed(() =>
  navigation.value.filter(
    (item) =>
      ![
        'home',
        'groups',
        'logs',
        'usage',
        ...(session.state.principalType === 'access_key' ? ['models'] : []),
      ].includes(item.id),
  ),
)
const failedNavigation = ref<string | null>(null)
const loggingOut = ref(false)
const { title: pageTitle } = usePageTitle()
const pageRefresh = providePageRefresh()
const fetching = useIsFetching()
const localActivity = provideLoadingActivity()
const navigating = ref(false)
const loadingPage = ref(true)
const mobileScrollPositions = new Map<string, number>()
let pendingMobileScroll: { path: string; top: number } | undefined
function scrollKey(fullPath: string): string {
  const url = new URL(fullPath, window.location.origin)
  url.searchParams.sort()
  return url.pathname + url.search
}
function cancelScrollRestore(): void {
  pendingMobileScroll = undefined
}
let navigationTarget: string | undefined
const contentPending = computed(
  () => pageRefresh.busy.value || fetching.value > 0 || localActivity.value,
)
const pageLoading = useLoadingFeedback(
  () => navigating.value || loadingPage.value || pageRefresh.running.value,
)
const removeBeforeEach = router.beforeEach((to, from) => {
  if (isMobile.value && to.path !== from.path) {
    const key = scrollKey(from.fullPath)
    mobileScrollPositions.delete(key)
    mobileScrollPositions.set(key, document.getElementById('modern-content')?.scrollTop ?? 0)
    if (mobileScrollPositions.size > 50)
      mobileScrollPositions.delete(mobileScrollPositions.keys().next().value!)
  }
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
      if (isMobile.value && pendingMobileScroll?.path === path) {
        document.getElementById('modern-content')?.scrollTo({ top: pendingMobileScroll.top })
        pendingMobileScroll = undefined
      }
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
const autoRefreshOptions = computed(() => [
  { value: '0', label: t('shell.autoRefreshOff') },
  ...autoRefreshIntervals.map((ms) => ({
    value: String(ms),
    label:
      ms < 60_000
        ? t('shell.autoRefreshSeconds', { count: ms / 1_000 })
        : t('shell.autoRefreshMinutes', { count: ms / 60_000 }),
  })),
])
function changeAutoRefresh(value: string): void {
  setAutoRefreshInterval(Number(value))
}
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
  const top = isMobile.value ? mobileScrollPositions.get(scrollKey(to.fullPath)) : undefined
  pendingMobileScroll = top === undefined ? undefined : { path: to.path, top }
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
    <div class="modern-main-column" :class="{ 'has-session-notice': requestBodyRecording }">
      <header
        class="modern-topbar"
        :class="{ 'has-auto-refresh': pageRefresh.autoAvailable.value }"
      >
        <DialogRoot v-model:open="mobileOpen">
          <DialogTrigger as-child>
            <AppIconButton class="modern-mobile-toggle" :icon="Menu" :label="t('navigation')" />
          </DialogTrigger>
          <AppDialogContent
            :placement="isMobile ? 'bottom' : 'sidebar'"
            :title="t('navigation')"
            :description="t('shell.mobileNavigationDescription')"
            @close-auto-focus="
              (event: Event) => {
                if (isMobile) {
                  event.preventDefault()
                  mobileNavigation?.focusMore()
                }
              }
            "
          >
            <AppDialogHeader
              :title="t('ui.mobile.more')"
              :close-label="t('shell.close')"
              @close="mobileOpen = false"
            />
            <div class="modern-mobile-menu">
              <nav :aria-label="t('navigation')">
                <RouterLink
                  v-for="item in moreNavigation"
                  :key="item.id"
                  :to="{ name: item.name }"
                  :aria-current="
                    (route.meta.primaryNav ?? route.name) === item.name ? 'page' : undefined
                  "
                  @click="mobileOpen = false"
                  ><AppIcon :icon="item.icon" /><span>{{ t(`pages.${item.id}.title`) }}</span
                  ><AppIcon :icon="ChevronRight" size="sm"
                /></RouterLink>
              </nav>
              <div class="modern-mobile-tools">
                <AppBadge
                  v-if="session.state.principalType === 'access_key'"
                  :icon="KeyRound"
                  tone="info"
                  >{{ t('auth.readOnly') }}</AppBadge
                ><AppearanceMenu /><AppButton
                  :icon="LogOut"
                  variant="ghost"
                  :loading="loggingOut"
                  @click="logout"
                  >{{ t('auth.logout') }}</AppButton
                >
              </div>
            </div>
          </AppDialogContent>
        </DialogRoot>
        <h1>{{ pageTitle }}</h1>
        <div v-if="pageRefresh.available.value" class="modern-topbar-refresh">
          <span v-if="refreshedAt">{{ refreshedAt }}</span>
          <AppIconButton
            :icon="RefreshCw"
            :label="t('shell.refresh')"
            :loading="pageRefresh.pending.value"
            :disabled="pageRefresh.busy.value"
            @click="refreshPage"
          />
          <AppSelectMenu
            v-if="pageRefresh.autoAvailable.value"
            :label="t('shell.autoRefresh')"
            :icon="Timer"
            :model-value="String(autoRefreshInterval)"
            :options="autoRefreshOptions"
            :show-value="autoRefreshInterval > 0"
            @update:model-value="changeAutoRefresh"
          />
        </div>
        <span
          v-if="pageRefresh.available.value"
          class="modern-topbar-divider"
          aria-hidden="true"
        ></span>
        <div class="modern-toolbar">
          <AppTooltip
            v-if="session.state.principalType === 'access_key'"
            :label="t('auth.readOnlyDescription')"
          >
            <AppBadge class="modern-session-scope" :icon="KeyRound" tone="info">
              {{ t('auth.readOnly') }}
            </AppBadge>
          </AppTooltip>
          <AppTooltip v-if="session.state.principalType === 'admin'" :label="t('groups.create')">
            <AppButton variant="brand" icon-only :aria-label="t('groups.create')" as-child>
              <RouterLink :to="createGroupLocation"
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
      <div v-if="requestBodyRecording" class="modern-session-notice">
        <AppNotice tone="warning" bordered>{{
          t('auth.requestBodyRecordingNotice', { days: requestBodyRecording.retentionDays })
        }}</AppNotice>
      </div>
      <main
        id="modern-content"
        :key="route.path"
        class="modern-content"
        tabindex="-1"
        @pointerdown="cancelScrollRestore"
        @wheel.passive="cancelScrollRestore"
        @keydown="cancelScrollRestore"
      >
        <slot />
      </main>
      <MobileNavigation ref="mobileNavigation" @more="mobileOpen = true" />
    </div>
  </div>
  <span class="modern-sr-only" role="status" aria-live="polite" aria-atomic="true">{{
    pageTitle
  }}</span>
</template>

<style scoped>
.modern-mobile-menu {
  padding: 0 var(--modern-space-5) var(--modern-space-4);
  overflow-y: auto;
}
.modern-mobile-menu nav {
  display: grid;
}
.modern-mobile-menu nav a {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  min-height: var(--modern-topbar-height);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}
.modern-mobile-menu nav a span {
  flex: 1;
}
.modern-mobile-menu nav a[aria-current] {
  color: var(--modern-accent);
}

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
/* 请求内容记录提示位于顶栏与内容之间，不随页面内容滚动。 */
.modern-main-column.has-session-notice {
  grid-template-rows: auto auto minmax(0, 1fr);
}
.modern-session-notice {
  padding: var(--modern-space-3) var(--modern-content-inset) 0;
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
.modern-mobile-toggle {
  display: none;
}
.modern-mobile-tools {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-1);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding-block: var(--modern-space-2);
  margin-bottom: var(--modern-space-2);
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
  padding: 0 var(--modern-content-inset);
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
  .modern-main-column {
    grid-template-rows: auto minmax(0, 1fr) auto;
  }
  .modern-main-column.has-session-notice {
    grid-template-rows: auto auto minmax(0, 1fr) auto;
  }
  .modern-toolbar {
    display: none;
  }
  .modern-topbar {
    gap: var(--modern-space-1);
    padding-inline: var(--modern-content-inset);
    padding-top: env(safe-area-inset-top);
  }
  /* 移动端顶栏只留刷新控件，时间文字和竖线让位给标题。 */
  .modern-topbar-refresh > span,
  .modern-topbar-divider {
    display: none;
  }
  .modern-topbar h1 {
    font-size: var(--modern-font-size-section);
  }
  .modern-topbar-refresh {
    gap: 0;
  }
  .modern-content {
    padding-bottom: env(safe-area-inset-bottom);
  }
}
.modern-notices {
  display: grid;
  flex: none;
  gap: var(--modern-space-3);
  margin-bottom: var(--modern-space-5);
}
</style>
