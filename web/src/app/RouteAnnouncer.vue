<script setup lang="ts">
import { computed, inject, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

import { authSessionKey } from '@/features/auth/auth-session'

const route = useRoute()
const session = inject(authSessionKey, null)
const { t } = useI18n()
const announcement = ref('')
const routeReady = computed(
  () => route.meta.requiresAuth !== true || session?.state.phase === 'validated',
)
let navigationSequence = 0

watch(
  [() => route.name, () => route.path, routeReady],
  async ([, , ready]) => {
    const sequence = ++navigationSequence
    announcement.value = ''
    if (!ready) return
    await nextTick()
    await new Promise<void>((resolve) => {
      requestAnimationFrame(() => resolve())
    })
    if (sequence !== navigationSequence) return

    const heading = document.querySelector<HTMLElement>('main h1')
    const main = document.querySelector<HTMLElement>('#main-content, main')
    const target = heading ?? main
    if (target) {
      if (!target.hasAttribute('tabindex')) target.setAttribute('tabindex', '-1')
      target.focus({ preventScroll: true })
    }

    const headingText = heading?.textContent?.trim()
    const titleKey = typeof route.meta.titleKey === 'string' ? route.meta.titleKey : ''
    announcement.value = headingText || (titleKey ? t(titleKey) : t('common.appName'))
  },
  { flush: 'post', immediate: true },
)
</script>

<template>
  <p class="sr-only" data-test="route-announcer" aria-live="polite" aria-atomic="true">
    {{ announcement }}
  </p>
</template>
