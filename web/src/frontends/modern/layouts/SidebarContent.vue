<script setup lang="ts">
import { BookOpen, Heart, Send } from '@lucide/vue'
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

import { navigationSections } from '@modern/app/navigation'
import { usePreferences } from '@modern/app/preferences'
import { useNavigation } from '@modern/app/use-navigation'
import BrandLogo from '@modern/components/BrandLogo.vue'
import GitHubIcon from '@modern/components/GitHubIcon.vue'
import AppTooltip from '@modern/components/ui/AppTooltip.vue'
import { AppExternalLink, AppIcon } from '@modern/components/ui'
import SystemStatus from '@modern/features/system/SystemStatus.vue'

defineProps<{ collapsed?: boolean }>()
const emit = defineEmits<{ navigate: [] }>()
const route = useRoute()
const { t } = useI18n()
const { resolvedTheme } = usePreferences()
const mascot = ref<InstanceType<typeof BrandLogo>>()
const mascotHint = useId()
const navigation = useNavigation()
const sections = computed(() =>
  navigationSections.filter((section) => navigation.value.some((item) => item.section === section)),
)
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
      :aria-describedby="mascotHint"
      @click="emit('navigate')"
      @keydown.space.prevent="!$event.repeat && mascot?.nudge()"
    >
      <BrandLogo ref="mascot" :compact="collapsed" :resolved-theme="resolvedTheme" />
    </RouterLink>
    <span :id="mascotHint" class="modern-sr-only">{{ t('shell.mascotHint') }}</span>
    <nav class="modern-sidebar-navigation" :aria-label="t('navigation')">
      <div v-for="section in sections" :key="section" class="modern-nav-section">
        <p v-if="!collapsed" class="modern-nav-section__label">{{ t(`sections.${section}`) }}</p>
        <AppTooltip
          v-for="item in navigation.filter((entry) => entry.section === section)"
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
        </AppTooltip>
      </div>
    </nav>
    <div class="modern-sidebar-footer">
      <div v-if="!collapsed" class="modern-footer-links">
        <AppExternalLink
          v-for="link in footerLinks"
          :key="link.href"
          class="modern-footer-link"
          :href="link.href"
        >
          <AppIcon :icon="link.icon" size="sm" />
          <span>{{ link.label }}</span>
        </AppExternalLink>
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
  padding: 0 var(--modern-space-3);
}
/* 品牌区与顶栏等高、与导航文字同一左边界，两栏顶部视觉基线才一致。 */
.modern-sidebar-brand {
  display: flex;
  min-height: var(--modern-topbar-height);
  align-items: center;
  padding-inline: var(--modern-space-3) 0;
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
  font-size: var(--modern-font-size-caption);
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
  font-size: var(--modern-font-size-body);
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
  inset-block: 25%;
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
  gap: var(--modern-space-1);
}
.modern-footer-link {
  display: flex;
  min-width: 0;
  min-height: var(--modern-control-sm);
  align-items: center;
  justify-content: center;
  gap: var(--modern-space-1-5);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-1-5) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-regular);
  white-space: nowrap;
}
/* 文字保持同一最小宽度，四个外链的图标才落在同一竖线上。 */
.modern-footer-link span {
  min-width: 5em;
}
.modern-footer-link:hover {
  background: var(--modern-surface);
  color: var(--modern-text);
}
.is-collapsed .modern-sidebar-brand {
  justify-content: flex-start;
  padding-inline: var(--modern-space-2) 0;
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
    font-size: var(--modern-font-size-section);
    min-height: var(--modern-touch-target);
  }
  .modern-footer-link {
    min-height: var(--modern-touch-target);
  }
}
</style>
