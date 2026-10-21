<script setup lang="ts">
import { Ellipsis } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { useNavigation } from '@modern/app/use-navigation'
import { AppIcon } from '@modern/components/ui'

defineEmits<{ more: [] }>()
const { t } = useI18n()
const route = useRoute()
const moreButton = ref<HTMLButtonElement>()
defineExpose({ focusMore: () => moreButton.value?.focus() })
const navigation = useNavigation()
const items = computed(() => {
  const workspace = navigation.value.some((item) => item.id === 'groups') ? 'groups' : 'models'
  return ['home', workspace, 'logs', 'usage'].flatMap((id) =>
    navigation.value.filter((item) => item.id === id),
  )
})
const active = computed(() => route.meta.primaryNav ?? route.name)
</script>

<template>
  <nav class="modern-mobile-navigation" :aria-label="t('navigation')">
    <RouterLink
      v-for="item in items"
      :key="item.id"
      :to="{ name: item.name }"
      :aria-current="active === item.name ? 'page' : undefined"
    >
      <AppIcon :icon="item.icon" />
      <span>{{ t(`ui.mobile.nav.${item.id}`) }}</span>
    </RouterLink>
    <button
      ref="moreButton"
      type="button"
      :class="{ 'is-active': !items.some((item) => item.name === active) }"
      @click="$emit('more')"
    >
      <AppIcon :icon="Ellipsis" /><span>{{ t('ui.mobile.more') }}</span>
    </button>
  </nav>
</template>

<style scoped>
.modern-mobile-navigation {
  display: none;
}
@media (max-width: 760px) {
  .modern-mobile-navigation {
    display: grid;
    grid-template-columns: repeat(5, minmax(0, 1fr));
    background: var(--modern-surface);
    border-top: var(--modern-line-width) solid var(--modern-border);
    padding: var(--modern-space-1) var(--modern-space-2)
      max(var(--modern-space-1), env(safe-area-inset-bottom));
  }
  .modern-mobile-navigation > * {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    min-height: var(--modern-topbar-height);
    gap: var(--modern-space-1);
    border: 0;
    border-radius: var(--modern-radius-control);
    padding: var(--modern-space-1);
    background: transparent;
    color: var(--modern-muted);
    font-size: var(--modern-font-size-caption);
  }
  .modern-mobile-navigation [aria-current],
  .modern-mobile-navigation .is-active {
    color: var(--modern-accent);
    background: var(--modern-accent-soft);
    font-weight: var(--modern-weight-semibold);
  }
}
</style>
