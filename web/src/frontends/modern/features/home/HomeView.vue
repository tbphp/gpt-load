<script setup lang="ts">
import { ArrowRight, Layers2 } from '@lucide/vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { navigationItems } from '@modern/app/navigation'
import PageHeader from '@modern/components/PageHeader.vue'
const { t } = useI18n()
const sections = ['workspace', 'observe'] as const
</script>

<template>
  <div class="modern-page">
    <PageHeader :title="t('pages.home.title')" :description="t('pages.home.description')">
      <RouterLink class="modern-button modern-button--primary" :to="{ name: 'modern-groups' }"
        ><Layers2 :size="16" aria-hidden="true" />{{ t('home.manageGroups') }}</RouterLink
      >
    </PageHeader>
    <div class="modern-overview-grid">
      <section v-for="section in sections" :key="section" class="modern-panel">
        <header class="modern-panel-header">
          <h2>{{ t(`home.${section}.title`) }}</h2>
          <p>{{ t(`home.${section}.description`) }}</p>
        </header>
        <div class="modern-launcher-list">
          <RouterLink
            v-for="item in navigationItems.filter(
              (entry) => entry.section === section && entry.id !== 'home',
            )"
            :key="item.id"
            class="modern-launcher"
            :to="{ name: item.name }"
          >
            <span class="modern-launcher-icon"
              ><component :is="item.icon" :size="21" :stroke-width="1.65" aria-hidden="true"
            /></span>
            <span class="modern-launcher-copy"
              ><strong>{{ t(`pages.${item.id}.title`) }}</strong
              ><span>{{ t(`pages.${item.id}.description`) }}</span></span
            ><ArrowRight :size="17" aria-hidden="true" />
          </RouterLink>
        </div>
      </section>
    </div>
    <section class="modern-workflow-strip" :aria-label="t('home.workflowTitle')">
      <div>
        <strong>{{ t('home.workflowTitle') }}</strong>
        <p>{{ t('home.workflowDescription') }}</p>
      </div>
      <ol>
        <li><span>1</span>{{ t('home.workflowGroups') }}</li>
        <li><span>2</span>{{ t('home.workflowCredentials') }}</li>
        <li><span>3</span>{{ t('home.workflowAccess') }}</li>
      </ol>
    </section>
  </div>
</template>
