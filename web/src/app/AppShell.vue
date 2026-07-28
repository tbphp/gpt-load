<script setup lang="ts">
import {
  Activity,
  House,
  KeyRound,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Settings,
  Sun,
  Upload,
} from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isNavigationFailure, RouterLink, useRoute, useRouter } from 'vue-router'

import AppDrawer from '@/components/ui/AppDrawer.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { useAuthSession } from '@/features/auth/auth-session'
import { useImportRecovery } from '@/features/import/import-recovery'
import { useUnsavedChangesController } from '@/app/unsaved-changes'
import { useTheme, type AppTheme } from '@/features/preferences/theme'
import { supportedLocales, type AppLocale } from '@/i18n'
import { useAppI18n } from '@/i18n/context'

const session = useAuthSession()
const recovery = useImportRecovery()
const unsavedChanges = useUnsavedChangesController()
const appI18n = useAppI18n()
const theme = useTheme()
const route = useRoute()
const router = useRouter()
const { locale, t } = useI18n()
const drawerOpen = ref(false)

const navigation = computed(() => [
  { key: 'home', to: '/', label: t('shell.home'), icon: House },
  { key: 'access-keys', to: '/access-keys', label: t('shell.accessKeys'), icon: KeyRound },
  { key: 'monitor', to: '/monitor', label: t('shell.monitor'), icon: Activity },
  { key: 'settings', to: '/settings', label: t('shell.settings'), icon: Settings },
])
const localeOptions = computed(() => [
  { value: 'zh-CN', label: t('shell.localeZh') },
  { value: 'en-US', label: t('shell.localeEn') },
  { value: 'ja-JP', label: t('shell.localeJa') },
])
const themeOptions = computed<Array<{ value: AppTheme; label: string; icon: typeof Sun }>>(() => [
  { value: 'system', label: t('shell.useSystemTheme'), icon: Monitor },
  { value: 'light', label: t('shell.useLightTheme'), icon: Sun },
  { value: 'dark', label: t('shell.useDarkTheme'), icon: Moon },
])

function isPrimaryActive(key: string): boolean {
  return route.meta.primaryNav === key
}

function setLocale(value: string): void {
  if (supportedLocales.includes(value as AppLocale)) {
    void appI18n.setLocale(value as AppLocale)
  }
}

async function logout(): Promise<void> {
  drawerOpen.value = false
  const bypassDirtyImport = route.name === 'import'
  if (bypassDirtyImport) {
    recovery.clear()
    unsavedChanges.bypassNext()
    session.clear()
    try {
      await router.replace({ name: 'login' })
    } finally {
      unsavedChanges.consumeBypass()
    }
    return
  }

  const failure = await router.replace({ name: 'login' })
  if (isNavigationFailure(failure)) return
  recovery.clear()
  session.clear()
}

watch(
  [() => route.meta.titleKey, locale],
  () => {
    const titleKey = route.meta.titleKey
    document.title = titleKey ? `${t(titleKey)} · ${t('common.appName')}` : t('common.appName')
  },
  { immediate: true },
)
</script>

<template>
  <div class="app-shell">
    <a class="skip-link" href="#main-content">{{ t('shell.skip') }}</a>
    <header class="app-topbar">
      <RouterLink class="brand" to="/" :aria-label="`${t('common.appName')} · ${t('shell.home')}`">
        <span class="brand-mark" aria-hidden="true"></span>
        <span>{{ t('common.appName') }}</span>
      </RouterLink>

      <nav class="desktop-nav" :aria-label="t('shell.primaryNavigation')">
        <RouterLink
          v-for="item in navigation"
          :key="item.key"
          class="nav-link"
          :class="{ 'nav-link--active': isPrimaryActive(item.key) }"
          :to="item.to"
          :aria-current="isPrimaryActive(item.key) ? 'page' : undefined"
        >
          <component :is="item.icon" :size="16" aria-hidden="true" />
          {{ item.label }}
        </RouterLink>
      </nav>

      <div class="shell-actions">
        <RouterLink class="button-link import-action" to="/import">
          <Upload :size="16" aria-hidden="true" />{{ t('shell.import') }}
        </RouterLink>
        <AppSelect
          class="shell-locale"
          :model-value="appI18n.getLocale()"
          :label="t('shell.language')"
          :options="localeOptions"
          @update:model-value="setLocale"
        />
        <div class="theme-actions" role="group" :aria-label="t('shell.themeSystem')">
          <IconButton
            v-for="option in themeOptions"
            :key="option.value"
            :label="option.label"
            :pressed="theme.theme.value === option.value"
            @click="theme.setTheme(option.value)"
          >
            <component :is="option.icon" :size="17" aria-hidden="true" />
          </IconButton>
        </div>
        <IconButton class="logout-action" :label="t('shell.signOut')" @click="logout">
          <LogOut :size="18" aria-hidden="true" />
        </IconButton>

        <AppDrawer
          v-model:open="drawerOpen"
          :title="t('shell.navigationTitle')"
          :description="t('shell.primaryNavigation')"
          :close-label="t('shell.closeNavigation')"
        >
          <template #trigger>
            <IconButton class="mobile-menu-trigger" :label="t('shell.openNavigation')">
              <Menu :size="20" aria-hidden="true" />
            </IconButton>
          </template>
          <nav class="mobile-nav" :aria-label="t('shell.primaryNavigation')">
            <RouterLink
              v-for="item in navigation"
              :key="item.key"
              class="mobile-nav__link"
              :class="{ 'mobile-nav__link--active': isPrimaryActive(item.key) }"
              :to="item.to"
              :aria-current="isPrimaryActive(item.key) ? 'page' : undefined"
              @click="drawerOpen = false"
            >
              <component :is="item.icon" :size="18" aria-hidden="true" />
              {{ item.label }}
            </RouterLink>
            <RouterLink
              class="mobile-nav__link mobile-nav__link--primary"
              to="/import"
              @click="drawerOpen = false"
            >
              <Upload :size="18" aria-hidden="true" />{{ t('shell.import') }}
            </RouterLink>
          </nav>
          <div class="mobile-preferences">
            <AppSelect
              :model-value="appI18n.getLocale()"
              :label="t('shell.language')"
              :options="localeOptions"
              @update:model-value="setLocale"
            />
            <div class="theme-actions" role="group" :aria-label="t('shell.themeSystem')">
              <IconButton
                v-for="option in themeOptions"
                :key="option.value"
                :label="option.label"
                :pressed="theme.theme.value === option.value"
                @click="theme.setTheme(option.value)"
              >
                <component :is="option.icon" :size="17" aria-hidden="true" />
              </IconButton>
            </div>
            <button
              class="mobile-sign-out"
              type="button"
              :aria-label="t('shell.signOut')"
              @click="logout"
            >
              <LogOut :size="18" aria-hidden="true" />{{ t('shell.signOut') }}
            </button>
          </div>
        </AppDrawer>
      </div>
    </header>

    <main id="main-content" class="app-content" tabindex="-1">
      <slot />
    </main>
  </div>
</template>

<style scoped>
.app-shell {
  min-height: 100vh;
}
.skip-link {
  position: fixed;
  z-index: var(--z-skip-link);
  top: var(--space-2);
  left: var(--space-2);
  transform: translateY(-160%);
  border-radius: var(--radius-control);
  background: var(--color-primary);
  color: var(--color-primary-ink);
  padding: var(--space-2) var(--space-3);
}
.skip-link:focus {
  transform: translateY(0);
}
.app-topbar {
  position: sticky;
  z-index: var(--z-sticky);
  top: 0;
  display: flex;
  min-height: 64px;
  align-items: center;
  gap: var(--space-6);
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
  padding: var(--space-2) max(var(--space-5), calc((100vw - 1440px) / 2));
}
.desktop-nav {
  display: flex;
  align-items: center;
  gap: var(--space-5);
}
.nav-link {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  border-bottom: 2px solid transparent;
  color: var(--color-text-muted);
  padding: var(--space-2) 2px;
}
.nav-link:hover,
.nav-link.router-link-active,
.nav-link--active {
  color: var(--color-text);
}
.nav-link.router-link-active,
.nav-link--active {
  border-bottom-color: var(--color-primary);
}
.shell-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-left: auto;
}
.button-link {
  gap: var(--space-2);
}
.theme-actions {
  display: flex;
  gap: var(--space-1);
}
.theme-actions :deep(.icon-button[aria-pressed='true']) {
  border-color: var(--color-primary);
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.mobile-menu-trigger {
  display: none;
}
.app-content {
  width: min(calc(100% - 40px), 1280px);
  margin: 0 auto;
  padding: var(--space-7) 0 var(--space-10);
}
.mobile-nav {
  display: grid;
  gap: var(--space-1);
}
.mobile-nav__link,
.mobile-sign-out {
  display: flex;
  min-height: 48px;
  align-items: center;
  gap: var(--space-3);
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text-muted);
  padding: var(--space-2) var(--space-3);
  font: inherit;
  cursor: pointer;
}
.mobile-nav__link.router-link-active,
.mobile-nav__link--active {
  background: var(--color-primary-soft);
  color: var(--color-primary);
}
.mobile-nav__link--primary {
  margin-top: var(--space-3);
  background: var(--color-primary);
  color: var(--color-primary-ink);
}
.mobile-preferences {
  display: grid;
  gap: var(--space-4);
  margin-top: var(--space-8);
  border-top: 1px solid var(--color-border);
  padding-top: var(--space-5);
}
@media (max-width: 1199px) {
  .desktop-nav,
  .import-action,
  .logout-action {
    display: none;
  }
  .mobile-menu-trigger {
    display: inline-flex;
  }
}
@media (max-width: 640px) {
  .app-topbar {
    flex-wrap: wrap;
  }
  .shell-actions {
    width: 100%;
    justify-content: space-between;
    margin-left: 0;
  }
  .shell-locale :deep(.app-select__trigger) {
    min-width: 118px;
  }
}
@media (max-width: 767px) {
  .app-topbar {
    padding-inline: var(--space-5);
  }
  .app-content {
    width: min(calc(100% - 32px), 1280px);
    padding-top: var(--space-5);
  }
}
</style>
