<script setup lang="ts">
import { CircleArrowUp } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { HomeBaseDto } from '@/app/resources/home'
import AppTooltip from '@/components/ui/AppTooltip.vue'
import { formatLocalInstant } from '@/lib/format'

const props = defineProps<{
  currentVersion: string
  update: NonNullable<HomeBaseDto['update']>
}>()

const { locale, t } = useI18n()
const label = computed(() =>
  t('home.ledger.updateAvailableLabel', { version: props.update.version }),
)
const tooltip = computed(() =>
  t('home.ledger.updateAvailableTooltip', {
    current: props.currentVersion,
    latest: props.update.version,
    published: formatLocalInstant(props.update.published_at_ms, locale.value),
  }),
)
</script>

<template>
  <AppTooltip :content="tooltip" side="bottom" align="end">
    <a
      class="home-release-update-link"
      :href="update.release_url"
      target="_blank"
      rel="noopener noreferrer"
      :aria-label="label"
    >
      <CircleArrowUp :size="14" stroke-width="2" aria-hidden="true" />
    </a>
  </AppTooltip>
</template>

<style scoped>
.home-release-update-link {
  display: inline-flex;
  width: 24px;
  height: 24px;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  color: var(--color-warning);
  transition:
    color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.home-release-update-link:hover {
  background: var(--color-warning-bg);
  color: var(--color-text);
}
</style>
