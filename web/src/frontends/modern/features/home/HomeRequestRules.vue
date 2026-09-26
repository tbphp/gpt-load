<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { HomeRequestRules } from '@modern/api/home'
import { AppBadge, AppIcon, AppPanel } from '@modern/components/ui'

const props = defineProps<{ rules: HomeRequestRules }>()
const { t } = useI18n()
const sections = computed(() =>
  [
    {
      id: 'redaction',
      title: t('home.requestRules.redaction'),
      count: props.rules.redaction.rules.length,
      status: 'configured',
    },
    {
      id: 'audit',
      title: t('home.requestRules.audit'),
      count: props.rules.audit.enabled ? props.rules.audit.rules.length : 0,
      status: 'enabled',
    },
  ].filter((section) => section.count > 0),
)
</script>

<template>
  <AppPanel v-if="sections.length" :title="t('home.requestRules.title')" compact flush>
    <details v-for="section in sections" :key="section.id" class="modern-request-rules-section">
      <summary class="modern-request-rules-summary">
        <span class="modern-request-rules-name">{{ section.title }}</span>
        <AppBadge tone="success" size="xs" dot>
          {{ t('home.requestRules.' + section.status) }}
        </AppBadge>
        <span class="modern-request-rules-count">
          {{ t('home.requestRules.count', { count: section.count }) }}
        </span>
        <AppIcon :icon="ChevronDown" size="sm" class="modern-request-rules-chevron" />
      </summary>
      <div class="modern-request-rules-body">
        <ol v-if="section.id === 'redaction'" class="modern-request-rules-list">
          <li v-for="(rule, index) in rules.redaction.rules" :key="index">
            <div class="modern-request-rules-rule-heading">
              <span>{{ t('home.requestRules.rule', { count: index + 1 }) }}</span>
              <AppBadge tone="brand" size="xs">
                {{ t('home.requestRules.' + rule.mode) }}
              </AppBadge>
            </div>
            <dl class="modern-request-rules-fields">
              <dt>{{ t('home.requestRules.pattern') }}</dt>
              <dd>
                <pre class="modern-request-rules-code"><code>{{ rule.pattern }}</code></pre>
              </dd>
              <template v-if="rule.mode === 'replace'">
                <dt>{{ t('home.requestRules.replacement') }}</dt>
                <dd>
                  <pre class="modern-request-rules-code"><code>{{
                    rule.replacement || t('home.requestRules.emptyReplacement')
                  }}</code></pre>
                </dd>
              </template>
            </dl>
          </li>
        </ol>
        <ol v-else class="modern-request-rules-list">
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
      </div>
    </details>
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
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-request-rules-list > li {
  min-width: 0;
}
.modern-request-rules-rule-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--modern-space-2);
  margin-bottom: var(--modern-space-2);
  font-weight: var(--modern-weight-medium);
  overflow-wrap: anywhere;
}
.modern-request-rules-fields {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--modern-space-2) var(--modern-space-3);
  align-items: baseline;
  margin: 0;
  font-size: var(--modern-font-size-small);
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
.modern-request-rules-code,
.modern-request-rules-instructions {
  margin: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  padding: var(--modern-space-2) var(--modern-space-3);
  color: var(--modern-text);
  font-size: var(--modern-font-size-small);
  line-height: var(--modern-leading-body);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
