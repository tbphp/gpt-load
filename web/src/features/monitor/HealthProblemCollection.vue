<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HealthProblemKeyDto } from '@/app/resources/health'
import { groupDetailLocation } from '@/app/route-locations'
import ProblemKeyRow from '@/components/health/ProblemKeyRow.vue'

interface ProblemSection {
  kind: string
  label: string
  tone: 'warning' | 'danger'
  keys: HealthProblemKeyDto[]
}

defineProps<{
  sections: ProblemSection[]
  expandedKeyIds: Set<number>
  remainingByKey: Record<number, string>
}>()
const emit = defineEmits<{ toggle: [keyID: number] }>()
const { locale, t } = useI18n()
const problemLabels = computed(() => ({
  consecutiveFailures: t('monitor.health.details.consecutiveFailureCount'),
  failureCategory: t('monitor.health.details.failureCategory'),
  statusCode: t('monitor.health.details.statusCode'),
  statusUnavailable: t('monitor.health.details.statusUnavailable'),
  recoversAt: t('monitor.health.recovery.recoversAt'),
  validationProbe: t('monitor.health.recovery.validationProbe'),
}))

function recoveryModeLabel(mode: string): string {
  if (mode === 'cooldown_expiry') return t('monitor.health.recovery.cooldownExpiry')
  if (mode === 'validation_probe') return t('monitor.health.recovery.validationProbe')
  return t('monitor.health.recovery.unknown')
}

function failureCategoryLabel(category: HealthProblemKeyDto['last_failure_category']): string {
  return t(`monitor.health.failureCategories.${category}`)
}
</script>

<template>
  <section class="health-section" aria-labelledby="health-problems-heading">
    <header class="health-section__heading">
      <div>
        <h2 id="health-problems-heading">{{ t('monitor.health.problems.title') }}</h2>
        <p>{{ t('monitor.health.problems.description') }}</p>
      </div>
    </header>

    <p v-if="sections.every((section) => section.keys.length === 0)" class="health-empty">
      {{ t('monitor.health.problems.empty') }}
    </p>
    <div v-else class="problem-sections">
      <section v-for="section in sections" :key="section.kind" class="problem-section">
        <h3>{{ section.label }}</h3>
        <p v-if="section.keys.length === 0" class="health-empty">
          {{ t('monitor.health.problems.noneForStatus') }}
        </p>
        <article
          v-for="key in section.keys"
          :key="key.key_id"
          class="problem-key"
          :data-key-id="key.key_id"
        >
          <div class="problem-key__summary">
            <RouterLink class="group-link" :to="groupDetailLocation(key.group_id, { tab: 'keys' })">
              {{ key.group_name }} · #{{ key.group_id }}
            </RouterLink>
            <span
              v-if="key.cooldown_until"
              class="problem-key__remaining"
              :data-test="`remaining-${key.key_id}`"
            >
              {{ t('monitor.health.problems.remaining', { time: remainingByKey[key.key_id] }) }}
            </span>
            <button
              type="button"
              class="problem-key__toggle"
              :data-test="`problem-key-${key.key_id}`"
              :aria-expanded="expandedKeyIds.has(key.key_id)"
              :aria-controls="`problem-key-details-${key.key_id}`"
              @click="emit('toggle', key.key_id)"
            >
              <span class="problem-key__identity">
                {{ t('monitor.health.problems.keyId', { id: key.key_id }) }}
              </span>
              <ChevronDown
                class="problem-key__chevron"
                :class="{
                  'problem-key__chevron--expanded': expandedKeyIds.has(key.key_id),
                }"
                :size="18"
                aria-hidden="true"
              />
            </button>
          </div>

          <ProblemKeyRow
            :problem-key="key"
            :tone="section.tone"
            :status-label="section.label"
            :failure-category-label="failureCategoryLabel(key.last_failure_category)"
            :labels="problemLabels"
            :locale="locale"
          />

          <div
            v-if="expandedKeyIds.has(key.key_id)"
            :id="`problem-key-details-${key.key_id}`"
            class="problem-key__details"
            :data-test="`problem-key-details-${key.key_id}`"
          >
            <dl class="detail-grid">
              <div>
                <dt>{{ t('monitor.health.details.failureCount') }}</dt>
                <dd :data-test="`failure-count-${key.key_id}`">{{ key.failure_count }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.health.details.recentSuccessCount') }}</dt>
                <dd>{{ key.recent_success_count }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.health.details.recentFailureCount') }}</dt>
                <dd>{{ key.recent_failure_count }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.health.details.consecutiveFailureCount') }}</dt>
                <dd>{{ key.consecutive_failure_count }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.health.details.manualWeight') }}</dt>
                <dd>{{ key.weight_manual ?? t('monitor.health.details.automatic') }}</dd>
              </div>
              <div>
                <dt>{{ t('monitor.health.details.autoWeight') }}</dt>
                <dd :data-test="`auto-weight-${key.key_id}`">{{ key.weight_auto }}</dd>
              </div>
            </dl>
            <div class="recovery-facts">
              <span>{{
                key.recovery.automatic
                  ? t('monitor.health.recovery.automatic')
                  : t('monitor.health.recovery.notAutomatic')
              }}</span>
              <span>{{ recoveryModeLabel(key.recovery.mode) }}</span>
              <time v-if="key.recovery.at" :datetime="key.recovery.at">
                {{ key.recovery.at }}
              </time>
              <span v-else-if="key.recovery.mode === 'validation_probe'">
                {{ t('monitor.health.recovery.runtimeDecides') }}
              </span>
            </div>
          </div>
        </article>
      </section>
    </div>
  </section>
</template>

<style scoped>
.health-section,
.problem-sections {
  display: grid;
  gap: var(--space-5);
}
.health-section__heading {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
}
.health-section__heading h2,
.problem-section h3 {
  margin: 0;
  font-size: 1rem;
}
.health-section__heading p {
  margin: var(--space-1) 0 0;
  color: var(--color-text-muted);
}
.health-empty {
  margin: 0;
  border: 1px dashed var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: var(--space-4);
}
.problem-section {
  display: grid;
  gap: var(--space-3);
}
.problem-key {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  background: var(--color-surface);
}
.problem-key__toggle {
  display: inline-flex;
  min-height: 44px;
  align-items: center;
  gap: var(--space-2);
  margin-left: auto;
  border: 0;
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  padding: var(--space-2);
  cursor: pointer;
}
.problem-key__identity,
.problem-key__remaining,
.detail-grid dd {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.problem-key__identity {
  font-weight: 700;
}
.problem-key__chevron {
  transition: transform var(--duration-fast) ease;
}
.problem-key__chevron--expanded {
  transform: rotate(180deg);
}
.problem-key__summary {
  display: flex;
  min-width: 0;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-start;
  gap: var(--space-4);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-3) var(--space-4);
}
.group-link {
  display: inline-flex;
  min-width: 44px;
  max-width: 100%;
  min-height: 44px;
  align-items: center;
  color: var(--color-action);
  font-weight: 700;
  overflow-wrap: anywhere;
  text-decoration: underline;
  text-decoration-color: transparent;
  text-underline-offset: 3px;
}
.group-link:hover {
  text-decoration-color: currentColor;
}
.problem-key__remaining {
  color: var(--color-text-faint);
}
.problem-key__details {
  display: grid;
  gap: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  padding: var(--space-4);
}
.detail-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: var(--space-3);
  margin: 0;
}
.detail-grid dt {
  color: var(--color-text-muted);
  font-size: 0.8125rem;
}
.detail-grid dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
  font-weight: 700;
}
.recovery-facts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
.recovery-facts span,
.recovery-facts time {
  border-radius: var(--radius-tag);
  background: var(--color-tag);
  padding: 6px 10px;
}
@media (max-width: 759px) {
  .problem-key__summary {
    align-items: flex-start;
    flex-direction: column;
  }
  .problem-key__remaining {
    margin-left: 0;
  }
}
@media (prefers-reduced-motion: reduce) {
  .problem-key__chevron {
    transition: none;
  }
}
</style>
