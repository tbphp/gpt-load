<script setup lang="ts">
import { ArrowRight, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HealthProblemKeyDto } from '@/app/resources/health'
import ProblemKeyRow from '@/components/health/ProblemKeyRow.vue'

import { problemKeysLocation, type HomeProblemGroup } from './home-presenter'

defineProps<{ groups: HomeProblemGroup[] }>()
const { locale, t } = useI18n()
const labels = computed(() => ({
  consecutiveFailures: t('home.problems.consecutiveFailures'),
  failureCategory: t('home.problems.failureCategory'),
  statusCode: t('home.problems.statusCode'),
  statusUnavailable: t('home.problems.statusUnavailable'),
  recoversAt: t('home.problems.recoversAt'),
  validationProbe: t('home.problems.validationProbe'),
  failureUnit: t('home.problems.failureUnit'),
  automaticRecovery: t('home.problems.automaticRecovery'),
  probeRecovery: t('home.problems.probeRecovery'),
}))

function categoryLabel(category: HealthProblemKeyDto['last_failure_category']): string {
  return category
}
</script>

<template>
  <section
    class="home-problems"
    data-test="home-problem-groups"
    :aria-label="t('home.problems.title')"
  >
    <template v-for="group in groups" :key="group.groupId">
      <article
        v-if="group.cooldownKeys.length > 0"
        class="home-problem-panel home-problem-status--warning"
        data-problem-kind="cooldown"
        :data-problem-group-id="group.groupId"
      >
        <header class="home-problem-panel__header">
          <h3>
            <TriangleAlert :size="18" aria-hidden="true" />
            <span>
              {{ group.groupName }} ·
              {{ t('home.problems.cooldown', { count: group.cooldownKeys.length }) }}
            </span>
          </h3>
          <RouterLink data-test="home-problem-link" :to="problemKeysLocation(group.groupId)">
            {{ t('home.problems.viewKeys') }}
            <ArrowRight :size="16" aria-hidden="true" />
          </RouterLink>
        </header>
        <ProblemKeyRow
          v-for="key in group.cooldownKeys"
          :key="key.key_id"
          :problem-key="key"
          tone="warning"
          :status-label="t('home.problems.cooldownStatus')"
          :failure-category-label="categoryLabel(key.last_failure_category)"
          :labels="labels"
          :locale="locale"
          appearance="compact"
        />
      </article>

      <article
        v-if="group.blacklistedKeys.length > 0"
        class="home-problem-panel home-problem-status--danger"
        data-problem-kind="blacklisted"
        :data-problem-group-id="group.groupId"
      >
        <header class="home-problem-panel__header">
          <h3>
            <TriangleAlert :size="18" aria-hidden="true" />
            <span>
              {{ group.groupName }} ·
              {{ t('home.problems.blacklisted', { count: group.blacklistedKeys.length }) }}
            </span>
          </h3>
          <RouterLink data-test="home-problem-link" :to="problemKeysLocation(group.groupId)">
            {{ t('home.problems.viewKeys') }}
            <ArrowRight :size="16" aria-hidden="true" />
          </RouterLink>
        </header>
        <ProblemKeyRow
          v-for="key in group.blacklistedKeys"
          :key="key.key_id"
          :problem-key="key"
          tone="danger"
          :status-label="t('home.problems.blacklistedStatus')"
          :failure-category-label="categoryLabel(key.last_failure_category)"
          :labels="labels"
          :locale="locale"
          appearance="compact"
        />
      </article>
    </template>
  </section>
</template>

<style scoped>
.home-problems {
  display: grid;
  gap: var(--space-3);
}
.home-problem-panel {
  display: grid;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  padding: var(--space-2) var(--space-5);
}
.home-problem-status--warning {
  border-color: color-mix(in srgb, var(--color-warning) 18%, var(--color-warning-bg));
  background: var(--color-warning-bg);
}
.home-problem-status--danger {
  border-color: color-mix(in srgb, var(--color-danger) 16%, var(--color-danger-bg));
  background: var(--color-danger-bg);
}
.home-problem-panel__header {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
}
.home-problem-panel__header h3 {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  font-size: var(--text-md);
  font-weight: 700;
  overflow-wrap: anywhere;
}
.home-problem-status--warning .home-problem-panel__header h3 svg {
  color: var(--color-warning);
}
.home-problem-status--danger .home-problem-panel__header h3 svg {
  color: var(--color-danger);
}
.home-problem-panel__header a {
  display: inline-flex;
  min-height: var(--touch-target);
  align-items: center;
  gap: var(--space-1);
  color: var(--color-action);
  font-weight: 650;
}
.home-problem-panel :deep(.problem-key-row--compact + .problem-key-row--compact) {
  border-top: 1px solid var(--color-border-subtle);
}
@media (max-width: 520px) {
  .home-problem-panel {
    padding: var(--space-3);
  }
  .home-problem-panel__header {
    align-items: flex-start;
  }
}
</style>

<style>
.home-problems .problem-key-row--compact {
  grid-template-columns: 150px minmax(0, 1fr) auto;
  gap: var(--space-5);
  border-left: 0;
  padding: calc(var(--space-1) / 2) 0;
}
.home-problems .problem-key-row--compact > * {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--text-sm);
}
.home-problems .problem-key-row__compact-mask {
  overflow-wrap: anywhere;
}
.home-problems .problem-key-row__compact-summary,
.home-problems .problem-key-row__compact-recovery {
  color: var(--color-text-faint);
}
.home-problems .problem-key-row__compact-recovery {
  text-align: right;
  white-space: nowrap;
}
@media (max-width: 520px) {
  .home-problems .problem-key-row--compact {
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-1);
  }
  .home-problems .problem-key-row__compact-recovery {
    text-align: left;
  }
}
</style>
