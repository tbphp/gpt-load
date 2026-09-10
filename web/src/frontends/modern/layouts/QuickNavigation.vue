<script setup lang="ts">
import { ArrowRight, Search, X } from '@lucide/vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
} from 'reka-ui'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { navigationItems } from '@modern/app/navigation'

const { t } = useI18n()
const router = useRouter()
const open = ref(false)
const query = ref('')
const input = ref<HTMLInputElement | null>(null)
const selected = ref(0)
const results = computed(() => {
  const words = query.value.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  return navigationItems.filter((item) => {
    const text =
      `${t(`pages.${item.id}.title`)} ${t(`pages.${item.id}.description`)} ${item.path}`.toLocaleLowerCase()
    return words.every((word) => text.includes(word))
  })
})
const shortcut = /Mac|iPhone|iPad/u.test(navigator.platform) ? '⌘ K' : 'Ctrl K'
watch(open, () => {
  query.value = ''
  selected.value = 0
})
watch(results, () => {
  selected.value = 0
})
function onShortcut(event: KeyboardEvent): void {
  if (event.isComposing) return
  if (!(event.metaKey || event.ctrlKey) || event.altKey || event.key.toLowerCase() !== 'k') return
  event.preventDefault()
  open.value = !open.value
}
async function navigate(index: number): Promise<void> {
  const item = results.value[index]
  if (!item) return
  await router.push({ name: item.name })
  open.value = false
}
function onInputKey(event: KeyboardEvent): void {
  if (event.isComposing) return
  if (event.key === 'Enter') {
    event.preventDefault()
    void navigate(selected.value)
  }
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
  event.preventDefault()
  if (!results.value.length) return
  selected.value =
    (selected.value + (event.key === 'ArrowDown' ? 1 : -1) + results.value.length) %
    results.value.length
  document
    .getElementById(`modern-quick-nav-${selected.value}`)
    ?.scrollIntoView({ block: 'nearest' })
}
function focusInput(event: Event): void {
  event.preventDefault()
  input.value?.focus()
}
onMounted(() => window.addEventListener('keydown', onShortcut))
onBeforeUnmount(() => window.removeEventListener('keydown', onShortcut))
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogTrigger class="modern-quick-nav-trigger" :aria-label="t('quickNavigation.title')">
      <Search :size="17" :stroke-width="1.8" aria-hidden="true" />
      <span>{{ t('quickNavigation.placeholder') }}</span
      ><kbd>{{ shortcut }}</kbd>
    </DialogTrigger>
    <DialogPortal>
      <DialogOverlay class="modern-overlay" />
      <DialogContent class="modern-command-dialog" @open-auto-focus="focusInput">
        <DialogTitle class="modern-sr-only">{{ t('quickNavigation.title') }}</DialogTitle>
        <DialogDescription class="modern-sr-only">{{
          t('quickNavigation.description')
        }}</DialogDescription>
        <div class="modern-command-input">
          <Search :size="20" :stroke-width="1.8" aria-hidden="true" />
          <input
            ref="input"
            v-model="query"
            type="search"
            role="combobox"
            aria-autocomplete="list"
            aria-expanded="true"
            aria-controls="modern-navigation-options"
            :aria-activedescendant="results.length ? `modern-quick-nav-${selected}` : undefined"
            :placeholder="t('quickNavigation.placeholder')"
            :aria-label="t('quickNavigation.title')"
            autocomplete="off"
            @keydown="onInputKey"
          />
          <DialogClose class="modern-icon-button" :aria-label="t('shell.close')"
            ><X :size="18" aria-hidden="true"
          /></DialogClose>
        </div>
        <div class="modern-command-results">
          <p class="modern-command-caption">{{ t('quickNavigation.pages') }}</p>
          <div
            id="modern-navigation-options"
            role="listbox"
            :aria-label="t('quickNavigation.pages')"
          >
            <button
              v-for="(item, index) in results"
              :id="`modern-quick-nav-${index}`"
              :key="item.id"
              type="button"
              role="option"
              :aria-selected="selected === index"
              tabindex="-1"
              class="modern-command-result"
              :class="{ 'is-selected': selected === index }"
              @pointermove="selected = index"
              @focus="selected = index"
              @click="navigate(index)"
            >
              <component :is="item.icon" :size="18" :stroke-width="1.8" aria-hidden="true" />
              <span
                >{{ t(`pages.${item.id}.title`)
                }}<small>{{ t(`sections.${item.section}`) }}</small></span
              >
              <ArrowRight :size="16" aria-hidden="true" />
            </button>
          </div>
          <p v-if="!results.length" class="modern-command-empty" role="status">
            {{ t('quickNavigation.empty') }}
          </p>
        </div>
        <p class="modern-command-footer">{{ t('quickNavigation.keyboardHint') }}</p>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
