<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'

const route = useRoute()
const { t } = useI18n()
const announcement = ref('')
let navigationSequence = 0

watch(
  [() => route.name, () => route.path],
  async () => {
    const sequence = ++navigationSequence
    announcement.value = ''
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
