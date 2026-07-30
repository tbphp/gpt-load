<script setup lang="ts">
import { KeyRound, TriangleAlert } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { AccessKeyReferenceDto } from '@/app/resources/groups'
import { accessKeysLocation } from '@/app/route-locations'

defineProps<{ references: AccessKeyReferenceDto[] }>()
const { t } = useI18n()
</script>

<template>
  <div class="group-in-use" role="alert">
    <TriangleAlert :size="18" aria-hidden="true" />
    <div>
      <strong>{{ t('group.settings.delete.inUseTitle') }}</strong>
      <p>{{ t('group.settings.delete.inUseDescription') }}</p>
      <ul>
        <li v-for="reference in references" :key="reference.id">
          <KeyRound :size="15" aria-hidden="true" />
          <span>{{ reference.name }}</span>
          <code>#{{ reference.id }}</code>
        </li>
      </ul>
      <RouterLink
        data-test="group-in-use-access-keys"
        class="button-link button-link--secondary"
        :to="accessKeysLocation()"
      >
        {{ t('group.settings.delete.manageAccessKeys') }}
      </RouterLink>
    </div>
  </div>
</template>

<style scoped>
.group-in-use {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--space-3);
  border: 1px solid color-mix(in srgb, var(--color-warning) 38%, var(--color-border-subtle));
  border-radius: var(--radius-control);
  background: var(--color-warning-bg);
  color: var(--color-warning);
  padding: var(--space-3);
}
.group-in-use strong,
.group-in-use p {
  margin: 0;
}
.group-in-use p {
  margin-top: var(--space-1);
}
ul {
  display: grid;
  gap: var(--space-2);
  margin: var(--space-3) 0;
  padding: 0;
  list-style: none;
}
li {
  display: flex;
  min-height: 28px;
  align-items: center;
  gap: var(--space-2);
  color: var(--color-text);
}
code {
  color: var(--color-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}
.button-link {
  min-height: 44px;
}
</style>
