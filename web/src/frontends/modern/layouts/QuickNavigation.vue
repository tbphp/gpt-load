<script setup lang="ts">
import { ArrowRight, Search, X } from '@lucide/vue'
import { DialogClose, DialogRoot, DialogTrigger } from 'reka-ui'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useNavigation } from '@modern/app/use-navigation'
import AppDialogContent from '@modern/components/ui/AppDialogContent.vue'
import AppIcon from '@modern/components/ui/AppIcon.vue'
import AppIconButton from '@modern/components/ui/AppIconButton.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const open = ref(false)
const query = ref('')
const input = ref<HTMLInputElement | null>(null)
const selected = ref(0)
const navigation = useNavigation()
const results = computed(() => {
  const words = query.value.trim().toLocaleLowerCase().split(/\s+/u).filter(Boolean)
  return navigation.value.filter((item) => {
    const text =
      `${t(`pages.${item.id}.title`)} ${t(`sections.${item.section}`)} ${item.path}`.toLocaleLowerCase()
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
watch(
  () => route.fullPath,
  () => {
    open.value = false
  },
)
function onShortcut(event: KeyboardEvent): void {
  if (event.isComposing) return
  if (!(event.metaKey || event.ctrlKey) || event.altKey || event.key.toLowerCase() !== 'k') return
  event.preventDefault()
  if (event.repeat) return
  open.value = !open.value
}
async function navigate(index: number): Promise<void> {
  const item = results.value[index]
  if (!item) return
  try {
    await router.push({ name: item.name })
  } catch {
    // AppLayout 的 router.onError 已展示加载错误，关闭弹窗以露出恢复操作。
    open.value = false
    return
  }
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
      <AppIcon :icon="Search" />
      <span>{{ t('quickNavigation.placeholder') }}</span
      ><kbd>{{ shortcut }}</kbd>
    </DialogTrigger>
    <AppDialogContent
      :title="t('quickNavigation.title')"
      :description="t('quickNavigation.description')"
      @open-auto-focus="focusInput"
    >
      <div class="modern-command-input">
        <AppIcon :icon="Search" size="lg" />
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
        <DialogClose as-child><AppIconButton :icon="X" :label="t('shell.close')" /></DialogClose>
      </div>
      <div class="modern-command-results">
        <p class="modern-command-caption">{{ t('quickNavigation.pages') }}</p>
        <div id="modern-navigation-options" role="listbox" :aria-label="t('quickNavigation.pages')">
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
            <AppIcon :icon="item.icon" />
            <span
              >{{ t(`pages.${item.id}.title`)
              }}<small>{{ t(`sections.${item.section}`) }}</small></span
            >
            <AppIcon :icon="ArrowRight" size="sm" />
          </button>
        </div>
        <p v-if="!results.length" class="modern-command-empty" role="status">
          {{ t('quickNavigation.empty') }}
        </p>
      </div>
      <p class="modern-command-footer">{{ t('quickNavigation.keyboardHint') }}</p>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-quick-nav-trigger {
  display: flex;
  width: 246px;
  height: var(--modern-control-md);
  align-items: center;
  gap: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  padding: 0 var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-text-small);
  text-align: left;
}
.modern-quick-nav-trigger:hover {
  border-color: var(--modern-accent);
}
.modern-quick-nav-trigger kbd {
  margin-left: auto;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-small);
  padding: 0 var(--modern-space-1);
  font-family: inherit;
  font-size: var(--modern-text-caption);
  line-height: var(--modern-leading-body);
}
.modern-command-input {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-3) var(--modern-space-4);
  color: var(--modern-muted);
  flex-shrink: 0;
}
.modern-command-input input {
  width: 100%;
  min-width: 0;
  border: 0;
  background: transparent;
  padding: var(--modern-space-2) 0;
  color: var(--modern-text);
  font-size: var(--modern-text-body);
}
.modern-command-input input:focus {
  outline: none;
}
.modern-command-results {
  max-height: min(440px, 58dvh);
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--modern-space-2);
  min-height: 0;
}
.modern-command-caption {
  padding: var(--modern-space-1) var(--modern-space-2) var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-text-caption);
}
.modern-command-result {
  display: flex;
  width: 100%;
  align-items: center;
  gap: var(--modern-space-3);
  border: 0;
  border-radius: var(--modern-radius-control);
  background: transparent;
  padding: var(--modern-space-2) var(--modern-space-3);
  text-align: left;
}
.modern-command-result.is-selected {
  background: var(--modern-accent-soft);
  color: var(--modern-accent);
}
.modern-command-result > span {
  display: grid;
  flex: 1;
  gap: var(--modern-space-0-5);
  font-size: var(--modern-text-secondary);
}
.modern-command-result small {
  color: var(--modern-muted);
  font-size: var(--modern-text-caption);
}
.modern-command-footer {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-2) var(--modern-space-4);
  color: var(--modern-muted);
  font-size: var(--modern-text-caption);
  flex-shrink: 0;
}
.modern-command-empty {
  padding: var(--modern-space-6) var(--modern-space-3);
  color: var(--modern-muted);
  font-size: var(--modern-text-secondary);
}
@media (max-width: 1150px) {
  .modern-quick-nav-trigger {
    width: 180px;
  }
}
@media (max-width: 760px) {
  .modern-quick-nav-trigger {
    width: var(--modern-touch-target);
    height: var(--modern-touch-target);
    justify-content: center;
    border: 0;
    background: transparent;
    padding: 0;
  }
  .modern-quick-nav-trigger span,
  .modern-quick-nav-trigger kbd {
    display: none;
  }
  .modern-command-input input {
    font-size: var(--modern-text-input-mobile);
  }
}
</style>
