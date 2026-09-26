<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import type { HomeRequestRules } from '@/app/resources/home'
import DisclosurePanel from '@/components/ui/DisclosurePanel.vue'
import StatusBadge from '@/components/ui/StatusBadge.vue'
import HomeSectionHeading from './HomeSectionHeading.vue'

const props = defineProps<{ rules: HomeRequestRules }>()
const { t } = useI18n()
const titleId = useId()
const sections = computed(() => [
  {
    id: 'redaction',
    title: t('home.requestRules.redaction'),
    active: props.rules.redaction.rules.length > 0,
    count: props.rules.redaction.rules.length,
    status: props.rules.redaction.rules.length ? 'configured' : 'unconfigured',
  },
  {
    id: 'audit',
    title: t('home.requestRules.audit'),
    active: props.rules.audit.enabled,
    count: props.rules.audit.rules.length,
    status: props.rules.audit.enabled ? 'enabled' : 'notApplied',
  },
])
</script>

<template>
  <section class="request-rules" :aria-labelledby="titleId">
    <HomeSectionHeading :id="titleId" :title="t('home.requestRules.title')" />
    <template v-for="section in sections" :key="section.id">
      <DisclosurePanel v-if="section.active" :summary="section.title">
        <template #summary>
          <span class="request-rules__summary">
            <span>{{ section.title }}</span>
            <StatusBadge tone="success" size="compact">
              {{ t('home.requestRules.' + section.status) }}
            </StatusBadge>
            <span class="request-rules__count">
              {{ t('home.requestRules.count', { count: section.count }) }}
            </span>
          </span>
        </template>
        <ol v-if="section.id === 'redaction'" class="request-rules__list">
          <li v-for="(rule, index) in rules.redaction.rules" :key="index">
            <div class="request-rules__rule-heading">
              <span>{{ t('home.requestRules.rule', { count: index + 1 }) }}</span>
              <StatusBadge tone="neutral" size="compact">
                {{ t('home.requestRules.' + rule.mode) }}
              </StatusBadge>
            </div>
            <dl class="request-rules__fields">
              <dt>{{ t('home.requestRules.pattern') }}</dt>
              <dd>
                <code>{{ rule.pattern }}</code>
              </dd>
              <template v-if="rule.mode === 'replace'">
                <dt>{{ t('home.requestRules.replacement') }}</dt>
                <dd>
                  <code>{{ rule.replacement || t('home.requestRules.emptyReplacement') }}</code>
                </dd>
              </template>
            </dl>
          </li>
        </ol>
        <template v-else>
          <dl class="request-rules__fields request-rules__route">
            <dt>{{ t('home.requestRules.channel') }}</dt>
            <dd>{{ rules.audit.channel_name }}</dd>
            <dt>{{ t('home.requestRules.model') }}</dt>
            <dd>
              <code>{{ rules.audit.model }}</code>
            </dd>
          </dl>
          <ol class="request-rules__list">
            <li v-for="(rule, index) in rules.audit.rules" :key="index">
              <div class="request-rules__rule-heading">
                <span>{{ rule.name }}</span>
                <StatusBadge :tone="rule.action === 'block' ? 'danger' : 'warning'" size="compact">
                  {{ t('home.requestRules.' + rule.action) }}
                </StatusBadge>
              </div>
              <p class="request-rules__instructions">{{ rule.instructions }}</p>
            </li>
          </ol>
        </template>
      </DisclosurePanel>
      <div v-else class="request-rules__inactive">
        <span>{{ section.title }}</span>
        <StatusBadge tone="neutral" size="compact">
          {{ t('home.requestRules.' + section.status) }}
        </StatusBadge>
      </div>
    </template>
  </section>
</template>

<style scoped>
.request-rules {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-card);
  margin-block: var(--space-4);
  padding: var(--space-4);
  font-size: var(--text-sm);
}
.request-rules__summary,
.request-rules__inactive,
.request-rules__rule-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  min-width: 0;
  color: var(--color-text);
  overflow-wrap: anywhere;
}
.request-rules__inactive {
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-3);
}
.request-rules__count,
.request-rules__fields dt {
  color: var(--color-text-muted);
  font-weight: 400;
}
.request-rules__list {
  display: grid;
  gap: var(--space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}
.request-rules__rule-heading {
  margin-bottom: var(--space-2);
}
.request-rules__fields {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-2) var(--space-4);
  align-items: baseline;
  margin: 0;
}
.request-rules__fields dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}
.request-rules__fields code {
  font-family: var(--font-mono);
  white-space: pre-wrap;
}
.request-rules__route {
  margin-bottom: var(--space-4);
}
.request-rules__instructions {
  margin: 0;
  color: var(--color-text-muted);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
