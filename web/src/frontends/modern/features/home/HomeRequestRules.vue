<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { HomeRequestRules } from '@modern/api/home'
import { AppBadge, AppIcon, AppPanel } from '@modern/components/ui'

const props = defineProps<{ rules: HomeRequestRules }>()
const { t } = useI18n()
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
  <AppPanel :title="t('home.requestRules.title')" compact flush>
    <component
      :is="section.active ? 'details' : 'div'"
      v-for="section in sections"
      :key="section.id"
      class="modern-request-rules-section"
    >
      <component :is="section.active ? 'summary' : 'div'" class="modern-request-rules-summary">
        <span class="modern-request-rules-name">{{ section.title }}</span>
        <AppBadge :tone="section.active ? 'success' : 'neutral'" size="xs" dot>
          {{ t('home.requestRules.' + section.status) }}
        </AppBadge>
        <span v-if="section.active" class="modern-request-rules-count">
          {{ t('home.requestRules.count', { count: section.count }) }}
        </span>
        <AppIcon
          v-if="section.active"
          :icon="ChevronDown"
          size="sm"
          class="modern-request-rules-chevron"
        />
      </component>
      <div v-if="section.active" class="modern-request-rules-body">
        <ol v-if="section.id === 'redaction'" class="modern-request-rules-list">
          <li v-for="(rule, index) in rules.redaction.rules" :key="index">
            <div class="modern-request-rules-rule-heading">
              <span>{{ t('home.requestRules.rule', { count: index + 1 }) }}</span>
              <AppBadge tone="neutral" size="xs">
                {{ t('home.requestRules.' + rule.mode) }}
              </AppBadge>
            </div>
            <dl class="modern-request-rules-fields">
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
          <dl class="modern-request-rules-fields modern-request-rules-route">
            <dt>{{ t('home.requestRules.channel') }}</dt>
            <dd>{{ rules.audit.channel_name }}</dd>
            <dt>{{ t('home.requestRules.model') }}</dt>
            <dd>
              <code>{{ rules.audit.model }}</code>
            </dd>
          </dl>
          <ol class="modern-request-rules-list">
            <li v-for="(rule, index) in rules.audit.rules" :key="index">
              <div class="modern-request-rules-rule-heading">
                <span>{{ rule.name }}</span>
                <AppBadge :tone="rule.action === 'block' ? 'danger' : 'warning'" size="xs">
                  {{ t('home.requestRules.' + rule.action) }}
                </AppBadge>
              </div>
              <p class="modern-request-rules-instructions">{{ rule.instructions }}</p>
            </li>
          </ol>
        </template>
      </div>
    </component>
  </AppPanel>
</template>

<style scoped>
.modern-request-rules-section + .modern-request-rules-section {
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-request-rules-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2) var(--modern-space-3);
  min-height: var(--modern-touch-target);
  padding: var(--modern-space-3) var(--modern-space-4);
  list-style: none;
  font-size: var(--modern-font-size-secondary);
}
summary.modern-request-rules-summary {
  cursor: pointer;
}
.modern-request-rules-summary::-webkit-details-marker {
  display: none;
}
summary.modern-request-rules-summary:hover {
  background: var(--modern-control-hover);
}
summary.modern-request-rules-summary:focus-visible {
  outline: var(--modern-focus-width) solid var(--modern-accent);
  outline-offset: calc(-1 * var(--modern-focus-width));
}
.modern-request-rules-name {
  color: var(--modern-text);
  font-weight: var(--modern-weight-medium);
}
.modern-request-rules-count,
.modern-request-rules-fields dt {
  color: var(--modern-muted);
}
.modern-request-rules-chevron {
  margin-inline-start: auto;
  color: var(--modern-muted);
  transform: rotate(-90deg);
}
[open] > .modern-request-rules-summary .modern-request-rules-chevron {
  transform: none;
}
.modern-request-rules-body {
  min-width: 0;
  padding: var(--modern-space-1) var(--modern-space-4) var(--modern-space-4);
  font-size: var(--modern-font-size-secondary);
}
.modern-request-rules-list {
  display: grid;
  gap: var(--modern-space-4);
  padding: 0;
  list-style: none;
}
.modern-request-rules-rule-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  margin-bottom: var(--modern-space-2);
  overflow-wrap: anywhere;
}
.modern-request-rules-fields {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--modern-space-2) var(--modern-space-4);
  align-items: baseline;
}
.modern-request-rules-fields dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}
.modern-request-rules-fields code {
  font-family: var(--modern-font-mono);
  white-space: pre-wrap;
}
.modern-request-rules-route {
  margin-bottom: var(--modern-space-4);
}
.modern-request-rules-instructions {
  color: var(--modern-muted);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
