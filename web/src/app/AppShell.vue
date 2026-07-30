<script setup lang="ts">
import { Activity, House, KeyRound, Menu, Settings } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isNavigationFailure, RouterLink, useRoute, useRouter } from 'vue-router'

import {
  accessKeysLocation,
  homeLocation,
  importLocation,
  loginLocation,
  monitorLocation,
  pageRouteNames,
  settingsLocation,
} from '@/app/route-locations'
import { useUnsavedChangesController } from '@/app/unsaved-changes'
import AppDrawer from '@/components/ui/AppDrawer.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { useAuthSession } from '@/features/auth/auth-session'
import { useImportRecovery } from '@/features/import/import-recovery'
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
  { key: 'home', to: homeLocation(), label: t('shell.home'), icon: House },
  {
    key: 'access-keys',
    to: accessKeysLocation(),
    label: t('shell.accessKeys'),
    icon: KeyRound,
  },
  { key: 'monitor', to: monitorLocation(), label: t('shell.monitor'), icon: Activity },
  { key: 'settings', to: settingsLocation(), label: t('shell.settings'), icon: Settings },
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
  const bypassDirtyImport = route.name === pageRouteNames.import
  if (bypassDirtyImport) {
    recovery.clear()
    unsavedChanges.bypassNext()
    session.clear()
    try {
      await router.replace(loginLocation())
    } finally {
      unsavedChanges.consumeBypass()
    }
    return
  }

  const failure = await router.replace(loginLocation())
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
      <RouterLink
        class="brand"
        :to="homeLocation()"
        :aria-label="`${t('common.appName')} · ${t('shell.home')}`"
      >
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
        <RouterLink
          class="button-link import-action"
          :to="importLocation()"
          :aria-label="t('shell.import')"
        >
          <KeyRound :size="15" aria-hidden="true" />
          <span class="import-action__label">{{ t('shell.import') }}</span>
        </RouterLink>
        <PreferencesControl
          compact
          show-sign-out
          :locale="currentLocale"
          :theme="theme.theme.value"
          @update:locale="setLocale"
          @update:theme="theme.setTheme"
          @sign-out="logout"
        />

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
              :to="importLocation()"
              @click="drawerOpen = false"
            >
              <KeyRound :size="18" aria-hidden="true" />{{ t('shell.import') }}
            </RouterLink>
          </nav>
          <div class="mobile-preferences">
            <PreferencesControl
              show-sign-out
              :locale="currentLocale"
              :theme="theme.theme.value"
              @update:locale="setLocale"
              @update:theme="theme.setTheme"
              @sign-out="logout"
            />
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
.app-topbar {
  position: sticky;
  z-index: var(--z-sticky);
  top: 0;
  display: flex;
  height: var(--topbar-height);
  align-items: center;
  gap: 28px;
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-surface);
  padding: 0 var(--topbar-padding-inline);
}
.brand {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: 9px;
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 400;
  letter-spacing: -0.01em;
}
.brand-mark {
  width: 7px;
  height: 18px;
  flex: 0 0 7px;
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
  border-bottom: 1.5px solid transparent;
  color: var(--color-text-muted);
  padding: var(--space-2) 2px;
  font-size: 13px;
  font-weight: 400;
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
  border-bottom-color: var(--color-text);
  font-weight: 560;
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
  min-height: var(--control-compact);
  gap: 6px;
  border: 1px solid var(--color-action);
  background: var(--color-action);
  color: var(--color-action-ink);
  padding: 0 9px;
  font-size: var(--text-meta);
  font-weight: 560;
}
.shell-actions :deep(.icon-button) {
  width: var(--control-compact);
  height: var(--control-compact);
}
.shell-actions :deep(.icon-button:hover:not(:disabled)) {
  background: var(--color-surface-sunken);
}
.mobile-menu-trigger {
  display: none;
}
.app-content {
  min-height: calc(100vh - var(--topbar-height));
}
.mobile-nav {
  display: grid;
  gap: var(--space-1);
}
.mobile-nav__link {
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
  color: var(--color-action-ink);
}
.mobile-nav__link--primary.router-link-active {
  background: var(--color-action);
  color: var(--color-action-ink);
}
.mobile-preferences {
  display: grid;
  gap: var(--space-4);
  margin-top: var(--space-8);
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-5);
}
@media (max-width: 860px) {
  .app-topbar {
    padding-inline: var(--space-4);
  }
  .desktop-nav,
  .shell-actions :deep(.preferences-control--compact) {
    display: none;
  }
  .import-action {
    width: var(--control-compact);
    padding: 0;
  }
  .import-action__label {
    display: none;
  }
  .mobile-menu-trigger {
    display: inline-flex;
  }
}
</style>
