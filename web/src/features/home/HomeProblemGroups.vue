<script setup lang="ts">
import { ArrowRight } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HealthProblemKeyDto } from '@/app/resources/health'
import ProblemKeyRow from '@/components/health/ProblemKeyRow.vue'

import { problemKeysLocation, type HomeProblemGroup } from './home-presenter'

defineProps<{ groups: HomeProblemGroup[] }>()
const { locale, t } = useI18n()
const labels = computed(() => ({
  consecutiveFailures: t('monitor.health.details.consecutiveFailureCount'),
  failureCategory: t('monitor.health.details.failureCategory'),
  statusCode: t('monitor.health.details.statusCode'),
  statusUnavailable: t('monitor.health.details.statusUnavailable'),
  recoversAt: t('monitor.health.recovery.recoversAt'),
  validationProbe: t('monitor.health.recovery.validationProbe'),
}))

function categoryLabel(category: HealthProblemKeyDto['last_failure_category']): string {
  return t(`monitor.health.failureCategories.${category}`)
}
</script>

<template>
  <section
    class="home-problems"
    data-test="home-problem-groups"
    aria-labelledby="home-problems-title"
  >
    <header class="home-section-heading">
      <div>
        <h2 id="home-problems-title">{{ t('home.problems.title') }}</h2>
        <p>{{ t('home.problems.description') }}</p>
      </div>
    </header>

    <article
      v-for="group in groups"
      :key="group.groupId"
      class="home-problem-group"
      :data-problem-group-id="group.groupId"
    >
      <header class="home-problem-group__header">
        <h3>{{ group.groupName }}</h3>
        <RouterLink data-test="home-problem-link" :to="problemKeysLocation(group.groupId)">
          {{ t('home.problems.viewKeys') }}
          <ArrowRight :size="16" aria-hidden="true" />
        </RouterLink>
      </header>

      <section
        v-if="group.cooldownKeys.length > 0"
        class="home-problem-status home-problem-status--warning"
        data-problem-kind="cooldown"
      >
        <h4>{{ t('home.problems.cooldown', { count: group.cooldownKeys.length }) }}</h4>
        <ProblemKeyRow
          v-for="key in group.cooldownKeys"
          :key="key.key_id"
          :problem-key="key"
          tone="warning"
          :status-label="t('monitor.health.problems.cooldown')"
          :failure-category-label="categoryLabel(key.last_failure_category)"
          :labels="labels"
          :locale="locale"
        />
      </section>

      <section
        v-if="group.blacklistedKeys.length > 0"
        class="home-problem-status home-problem-status--danger"
        data-problem-kind="blacklisted"
      >
        <h4>{{ t('home.problems.blacklisted', { count: group.blacklistedKeys.length }) }}</h4>
        <ProblemKeyRow
          v-for="key in group.blacklistedKeys"
          :key="key.key_id"
          :problem-key="key"
          tone="danger"
          :status-label="t('monitor.health.problems.blacklisted')"
          :failure-category-label="categoryLabel(key.last_failure_category)"
          :labels="labels"
          :locale="locale"
        />
      </section>
    </article>
  </section>
</template>

<style scoped>
.home-problems,
.home-problem-group {
  display: grid;
  gap: var(--space-4);
}
.home-section-heading h2,
.home-section-heading p,
.home-problem-group h3,
.home-problem-status h4 {
  margin: 0;
}
.home-section-heading h2 {
  font-family: var(--font-serif);
  font-size: 1.45rem;
  font-weight: 500;
}
.home-section-heading p {
  margin-top: var(--space-1);
  color: var(--color-text-muted);
}
.home-problem-group {
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
}
.home-problem-group__header {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-4);
}
.home-problem-group__header h3 {
  overflow-wrap: anywhere;
}
.home-problem-group__header a {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-1);
  color: var(--color-action);
  font-weight: 650;
}
.home-problem-status {
  display: grid;
  gap: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
  padding: var(--space-3);
}
.home-problem-status--warning {
  background: var(--color-warning-bg);
}
.home-problem-status--danger {
  background: var(--color-danger-bg);
}
.home-problem-status h4 {
  padding-inline: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}
.home-problem-status :deep(.problem-key-row) {
  border-radius: var(--radius-control);
  background: var(--color-surface);
}
</style>
