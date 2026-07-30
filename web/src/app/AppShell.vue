<script setup lang="ts">
import { Activity, House, KeyRound, LogOut, Menu, Settings, Upload } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isNavigationFailure, RouterLink, useRoute, useRouter } from 'vue-router'

import AppDrawer from '@/components/ui/AppDrawer.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { useAuthSession } from '@/features/auth/auth-session'
import { useImportRecovery } from '@/features/import/import-recovery'
import { useUnsavedChangesController } from '@/app/unsaved-changes'
import PreferencesControl from '@/features/preferences/PreferencesControl.vue'
import { useTheme } from '@/features/preferences/theme'
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
const currentLocale = computed(() => locale.value as AppLocale)

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
        <span class="brand-mark" data-test="ledger-brand-mark" aria-hidden="true"></span>
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
        <PreferencesControl
          compact
          :locale="currentLocale"
          :theme="theme.theme.value"
          @update:locale="setLocale"
          @update:theme="theme.setTheme"
        />
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
            <PreferencesControl
              :locale="currentLocale"
              :theme="theme.theme.value"
              @update:locale="setLocale"
              @update:theme="theme.setTheme"
            />
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
  background: var(--color-action);
  color: var(--color-text-inverse);
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
  min-height: 68px;
  align-items: center;
  gap: var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface);
  padding: var(--space-2)
    max(var(--page-gutter), calc((100vw - var(--content-max)) / 2 + var(--page-gutter)));
}
.brand {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-3);
  font-size: var(--text-lg);
  font-weight: 650;
}
.brand-mark {
  width: 8px;
  height: 28px;
  flex: 0 0 8px;
  border-radius: 1px;
  background: var(--color-action);
}
.desktop-nav {
  display: flex;
  align-items: center;
  gap: var(--space-5);
}
.desktop-nav :deep(svg) {
  display: none;
}
.nav-link {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  border-bottom: 2px solid transparent;
  color: var(--color-text-muted);
  padding: var(--space-2) 2px;
  transition:
    color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard);
}
.nav-link:hover,
.nav-link.router-link-active,
.nav-link--active {
  color: var(--color-text);
}
.nav-link.router-link-active,
.nav-link--active {
  border-bottom-color: var(--color-action);
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
.import-action {
  border: 1px solid var(--color-border-strong);
  background: transparent;
  color: var(--color-action);
}
.mobile-menu-trigger {
  display: none;
}
.app-content {
  width: min(calc(100% - (2 * var(--page-gutter))), var(--content-max));
  margin: 0 auto;
  padding: var(--space-8) 0 var(--space-10);
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
  background: var(--color-action-soft);
  color: var(--color-action);
}
.mobile-nav__link--primary {
  margin-top: var(--space-3);
  background: var(--color-action);
  color: var(--color-text-inverse);
}
.mobile-preferences {
  display: grid;
  gap: var(--space-4);
  margin-top: var(--space-8);
  border-top: 1px solid var(--color-border-subtle);
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
}
@media (max-width: 767px) {
  .app-topbar {
    padding-inline: var(--space-5);
  }
  .app-content {
    width: min(calc(100% - 32px), var(--content-max));
    padding-top: var(--space-5);
  }
}
</style>
