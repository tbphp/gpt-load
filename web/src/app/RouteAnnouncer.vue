<script setup lang="ts">
import { computed, inject, nextTick, onBeforeUnmount, ref, watch } from 'vue'
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
let headingObserver: MutationObserver | undefined

function stopHeadingObserver(): void {
  headingObserver?.disconnect()
  headingObserver = undefined
}

function headingText(heading: HTMLElement): string {
  return heading.textContent?.trim() ?? ''
}

function focusElement(target: HTMLElement): void {
  if (!target.hasAttribute('tabindex')) target.setAttribute('tabindex', '-1')
  target.focus({ preventScroll: true })
}

function canMoveFocusToMain(main: HTMLElement): boolean {
  const activeElement = document.activeElement
  return activeElement === main || activeElement === document.body || activeElement === null
}

function focusElementWithinMain(target: HTMLElement, main: HTMLElement): void {
  if (canMoveFocusToMain(main)) focusElement(target)
}

function fallbackAnnouncement(): string {
  const titleKey = typeof route.meta.titleKey === 'string' ? route.meta.titleKey : ''
  return titleKey ? t(titleKey) : t('common.appName')
}

onBeforeUnmount(() => {
  navigationSequence += 1
  stopHeadingObserver()
})

watch(
  [() => route.name, () => route.path, routeReady],
  async ([, , ready]) => {
    const sequence = ++navigationSequence
    stopHeadingObserver()
    announcement.value = ''
    if (!ready) return
    await nextTick()
    await new Promise<void>((resolve) => {
      requestAnimationFrame(() => resolve())
    })
    if (sequence !== navigationSequence) return

    const main = document.querySelector<HTMLElement>('#main-content, main')
    const heading = main?.querySelector<HTMLElement>('h1') ?? null
    const target = heading ?? main
    if (target && main) focusElementWithinMain(target, main)

    const initialHeadingText = heading === null ? '' : headingText(heading)
    announcement.value = initialHeadingText || fallbackAnnouncement()
    if (main === null || initialHeadingText !== '') return

    headingObserver = new MutationObserver(() => {
      if (sequence !== navigationSequence || !main.isConnected) {
        stopHeadingObserver()
        return
      }
      const asynchronousHeading = main.querySelector<HTMLElement>('h1')
      if (asynchronousHeading === null) return
      const text = headingText(asynchronousHeading)
      if (text === '') return

      focusElementWithinMain(asynchronousHeading, main)
      announcement.value = text
      stopHeadingObserver()
    })
    headingObserver.observe(main, {
      childList: true,
      subtree: true,
      characterData: true,
    })
  },
  { flush: 'post', immediate: true },
)
</script>

<template>
  <p class="sr-only" aria-live="polite" aria-atomic="true">
    {{ announcement }}
  </p>
</template>
