<script setup lang="ts">
import { BookOpen, Heart, Send } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

import { navigationItems, navigationSections } from '@modern/app/navigation'
import BrandLogo from '@modern/components/BrandLogo.vue'
import GitHubIcon from '@modern/components/GitHubIcon.vue'
import HintTooltip from '@modern/components/HintTooltip.vue'
import AppExternalLink from '@modern/components/ui/AppExternalLink.vue'
import AppIcon from '@modern/components/ui/AppIcon.vue'
import SystemStatus from '@modern/features/system/SystemStatus.vue'

defineProps<{ collapsed?: boolean }>()
const emit = defineEmits<{ navigate: [] }>()
const route = useRoute()
const { t } = useI18n()
const footerLinks = computed(() => [
  { label: t('shell.documentation'), href: 'https://www.gpt-load.com/docs', icon: BookOpen },
  { label: t('shell.sponsor'), href: 'https://www.gpt-load.com/sponsor', icon: Heart },
  { label: 'GitHub', href: 'https://github.com/tbphp/gpt-load', icon: GitHubIcon },
  { label: 'Telegram', href: 'https://t.me/+GHpy5SwEllg3MTUx', icon: Send },
])
</script>

<template>
  <div class="modern-sidebar-content" :class="{ 'is-collapsed': collapsed }">
    <RouterLink
      class="modern-sidebar-brand"
      :to="{ name: 'modern-home' }"
      :aria-label="t('shell.goHome')"
      @click="emit('navigate')"
    >
      <BrandLogo :compact="collapsed" />
    </RouterLink>
    <nav class="modern-sidebar-navigation" :aria-label="t('navigation')">
      <div v-for="section in navigationSections" :key="section" class="modern-nav-section">
        <p v-if="!collapsed" class="modern-nav-section__label">{{ t(`sections.${section}`) }}</p>
        <HintTooltip
          v-for="item in navigationItems.filter((entry) => entry.section === section)"
          :key="item.id"
          :label="t(`pages.${item.id}.title`)"
          :disabled="!collapsed"
          side="right"
        >
          <RouterLink
            class="modern-nav-link"
            :class="{ 'is-active': (route.meta.primaryNav ?? route.name) === item.name }"
            :to="{ name: item.name }"
            :aria-label="t(`pages.${item.id}.title`)"
            :aria-current="(route.meta.primaryNav ?? route.name) === item.name ? 'page' : undefined"
            @click="emit('navigate')"
          >
            <AppIcon :icon="item.icon" />
            <span v-if="!collapsed">{{ t(`pages.${item.id}.title`) }}</span>
          </RouterLink>
        </HintTooltip>
      </div>
    </nav>
    <div class="modern-sidebar-footer">
      <div class="modern-footer-links">
        <HintTooltip
          v-for="link in footerLinks"
          :key="link.href"
          :label="link.label"
          :disabled="!collapsed"
          side="right"
        >
          <AppExternalLink class="modern-footer-link" :href="link.href" :aria-label="link.label">
            <AppIcon :icon="link.icon" size="sm" />
            <span v-if="!collapsed">{{ link.label }}</span>
          </AppExternalLink>
        </HintTooltip>
      </div>
      <SystemStatus :collapsed="collapsed" />
    </div>
  </div>
</template>

<style scoped>
.modern-sidebar-content {
  display: flex;
  min-height: 100%;
  flex-direction: column;
  padding: var(--modern-space-3) var(--modern-space-3) 0;
}
.modern-sidebar-brand {
  display: flex;
  min-height: 60px;
  align-items: center;
  justify-content: center;
  margin-bottom: var(--modern-space-4);
}
.modern-sidebar-navigation {
  display: grid;
  gap: var(--modern-space-5);
}
.modern-nav-section {
  display: grid;
  gap: var(--modern-space-1);
}
.modern-nav-section__label {
  margin: 0 var(--modern-space-3) var(--modern-space-1-5);
  color: var(--modern-muted);
  font-size: var(--modern-text-caption);
  font-weight: var(--modern-weight-medium);
  letter-spacing: var(--modern-tracking-label);
}
.modern-nav-link {
  position: relative;
  display: flex;
  min-height: var(--modern-control-nav);
  align-items: center;
  gap: var(--modern-space-3);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-text-body);
  font-weight: var(--modern-weight-medium);
  white-space: nowrap;
}
.modern-nav-link:hover {
  background: var(--modern-surface);
  color: var(--modern-text);
}
.modern-nav-link.is-active {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
  font-weight: var(--modern-weight-semibold);
}
.modern-nav-link.is-active::before {
  position: absolute;
  top: 11px;
  bottom: 11px;
  left: calc(-1 * var(--modern-space-3));
  width: 3px;
  border-radius: 0 var(--modern-space-0-5) var(--modern-space-0-5) 0;
  background: var(--modern-coral);
  content: '';
}
.modern-sidebar-footer {
  display: grid;
  gap: var(--modern-space-0-5);
  margin-top: auto;
  padding-top: var(--modern-space-4);
}
.modern-footer-links {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--modern-space-0-5) var(--modern-space-1);
}
.modern-footer-link {
  display: flex;
  min-width: 0;
  min-height: var(--modern-control-sm);
  align-items: center;
  gap: var(--modern-space-1-5);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-1-5) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-text-small);
  font-weight: var(--modern-weight-regular);
  white-space: nowrap;
}
.modern-footer-link:hover {
  background: var(--modern-surface);
  color: var(--modern-text);
}
.is-collapsed .modern-footer-links {
  column-gap: 0;
}
.is-collapsed .modern-footer-link {
  justify-content: center;
  padding-inline: 0;
}
.is-collapsed .modern-sidebar-brand {
  margin-bottom: var(--modern-space-6);
}
.is-collapsed .modern-nav-link {
  justify-content: center;
  padding-inline: 0;
}
.is-collapsed .modern-nav-section + .modern-nav-section {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-4);
}
@media (max-width: 760px) {
  .modern-nav-link {
    font-size: var(--modern-text-section);
    min-height: var(--modern-touch-target);
  }
  .modern-footer-link {
    min-height: var(--modern-touch-target);
  }
  .modern-sidebar-brand {
    justify-content: flex-start;
  }
}
</style>
