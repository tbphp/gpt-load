<script setup lang="ts">
import { Check, KeyRound, Layers2, SlidersHorizontal } from '@lucide/vue'
import { computed, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import type { GroupRow } from '@modern/api/groups'
import { AppBadge, AppButton, AppIcon, AppPanel } from '@modern/components/ui'

const props = defineProps<{ groups: GroupRow[]; keyCount: number }>()
const { t, n } = useI18n()
interface SetupStep {
  key: 'group' | 'upstream' | 'key'
  icon: Component
  done: boolean
  to: RouteLocationRaw
}
const steps = computed<SetupStep[]>(() => {
  const configured = props.groups.some(
    (group) => group.credentials.total > 0 && group.modelCount > 0,
  )
  const target =
    props.groups.find((group) => group.enabled && group.credentials.total > 0) ??
    props.groups.find((group) => group.enabled) ??
    props.groups[0]
  return [
    {
      key: 'group',
      icon: Layers2,
      done: props.groups.length > 0,
      to: { name: 'modern-groups', query: { panel: 'create' } },
    },
    {
      key: 'upstream',
      icon: SlidersHorizontal,
      done: configured,
      to: target
        ? {
            name: 'modern-group-detail',
            params: { id: target.id },
            query: { panel: target.credentials.total ? 'models' : 'add' },
          }
        : { name: 'modern-groups' },
    },
    {
      key: 'key',
      icon: KeyRound,
      done: props.keyCount > 0,
      to: { name: 'modern-access-keys', query: { panel: 'create' } },
    },
  ]
})
const current = computed(() => steps.value.findIndex((step) => !step.done))
const completed = computed(() => steps.value.filter((step) => step.done).length)
</script>

<template>
  <AppPanel :title="t('home.setup.title')" compact>
    <template #actions>
      <span class="modern-home-setup-progress">{{
        t('home.setup.progress', { count: n(completed), total: n(steps.length) })
      }}</span>
    </template>
    <ol class="modern-home-setup-steps">
      <li
        v-for="(step, index) in steps"
        :key="step.key"
        class="modern-home-setup-step"
        :class="{ 'is-current': index === current, 'is-complete': step.done }"
        :aria-current="index === current ? 'step' : undefined"
      >
        <div class="modern-home-setup-heading">
          <span class="modern-home-setup-symbol">
            <AppIcon :icon="step.done ? Check : step.icon" size="md" />
          </span>
          <h3>{{ t('home.setup.' + step.key + '.title') }}</h3>
          <span class="modern-home-setup-number">{{ n(index + 1) }}</span>
        </div>
        <p>{{ t('home.setup.' + step.key + '.description') }}</p>
        <div class="modern-home-setup-action">
          <AppButton v-if="index === current" as-child variant="primary" size="sm">
            <RouterLink :to="step.to">{{ t('home.setup.' + step.key + '.action') }}</RouterLink>
          </AppButton>
          <AppBadge v-else size="xs" variant="plain" :tone="step.done ? 'info' : 'neutral'">{{
            t(step.done ? 'home.setup.complete' : 'home.setup.pending')
          }}</AppBadge>
        </div>
      </li>
    </ol>
  </AppPanel>
</template>

<style scoped>
.modern-home-setup-progress {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-home-setup-steps {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr));
  gap: var(--modern-space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.modern-home-setup-step {
  display: flex;
  flex-direction: column;
  min-width: 0;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid transparent;
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-4);
}
.modern-home-setup-step.is-current {
  border-color: var(--modern-segmented-active-border);
  background: var(--modern-accent-soft);
}
.modern-home-setup-heading {
  display: flex;
  align-items: center;
  min-width: 0;
  gap: var(--modern-space-2);
}
.modern-home-setup-heading h3 {
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-medium);
}
.modern-home-setup-symbol {
  display: grid;
  width: var(--modern-control-sm);
  height: var(--modern-control-sm);
  flex: none;
  place-items: center;
  border-radius: var(--modern-radius-control);
  background: var(--modern-control-hover);
  color: var(--modern-muted);
}
.is-current .modern-home-setup-symbol {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
.is-complete .modern-home-setup-symbol {
  background: var(--modern-info-soft);
  color: var(--modern-info);
}
.modern-home-setup-number {
  margin-inline-start: auto;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-home-setup-step p {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  line-height: var(--modern-leading-body);
}
.modern-home-setup-action {
  display: flex;
  align-items: center;
  min-height: var(--modern-control-sm);
  margin-top: auto;
}
</style>
