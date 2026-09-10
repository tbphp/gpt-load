<script setup lang="ts">
import { ArrowUpRight, BookOpen, CodeXml, Send, Settings2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'

import { navigationItems, navigationSections } from '@modern/app/navigation'
import BrandLogo from '@modern/components/BrandLogo.vue'
import HintTooltip from '@modern/components/HintTooltip.vue'

defineProps<{ collapsed?: boolean }>()
const emit = defineEmits<{ navigate: [] }>()
const route = useRoute()
const { t } = useI18n()
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
            <component :is="item.icon" :size="18" :stroke-width="1.8" aria-hidden="true" />
            <span v-if="!collapsed">{{ t(`pages.${item.id}.title`) }}</span>
          </RouterLink>
        </HintTooltip>
      </div>
    </nav>
    <div class="modern-sidebar-footer">
      <HintTooltip :label="t('settings')" side="right">
        <RouterLink
          class="modern-nav-link"
          :class="{ 'is-active': route.name === 'modern-settings' }"
          :to="{ name: 'modern-settings' }"
          :aria-label="t('settings')"
          :aria-current="route.name === 'modern-settings' ? 'page' : undefined"
          @click="emit('navigate')"
        >
          <Settings2 :size="18" :stroke-width="1.8" aria-hidden="true" />
          <span v-if="!collapsed">{{ t('settings') }}</span>
        </RouterLink>
      </HintTooltip>
      <HintTooltip :label="t('shell.documentation')" side="right">
        <a
          class="modern-nav-link"
          href="https://www.gpt-load.com"
          target="_blank"
          rel="noopener noreferrer"
          :aria-label="t('shell.documentation')"
        >
          <BookOpen :size="18" :stroke-width="1.8" aria-hidden="true" />
          <span v-if="!collapsed">{{ t('shell.documentation') }}</span>
          <ArrowUpRight
            v-if="!collapsed"
            class="modern-nav-link__external"
            :size="14"
            aria-hidden="true"
          />
        </a>
      </HintTooltip>
      <div class="modern-sidebar-meta">
        <HintTooltip label="GitHub" side="right">
          <a
            class="modern-community-link"
            href="https://github.com/tbphp/gpt-load"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="GitHub"
          >
            <CodeXml :size="15" :stroke-width="1.8" aria-hidden="true" />
            <template v-if="!collapsed"><span>GitHub</span><small>Star</small></template>
          </a>
        </HintTooltip>
        <HintTooltip label="Telegram" side="right">
          <a
            class="modern-community-link"
            href="https://t.me/+GHpy5SwEllg3MTUx"
            target="_blank"
            rel="noopener noreferrer"
            aria-label="Telegram"
          >
            <Send :size="14" :stroke-width="1.8" aria-hidden="true" />
            <span v-if="!collapsed">Telegram</span>
          </a>
        </HintTooltip>
      </div>
    </div>
  </div>
</template>
