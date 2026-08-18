<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccessProtocol } from '@/api/control/types'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'

import {
  clientGroup,
  gatewayClients,
  type GatewayClient,
  type GatewayClientGroup,
  type GatewayClientID,
} from './gateway-clients'

const props = defineProps<{
  modelValue: GatewayClientID
  protocols: readonly AccessProtocol[]
  disabled?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: GatewayClientID] }>()

const { t } = useI18n()
const identity = useId()
const listID = `${identity}-clients`
const open = ref(false)
const query = ref('')
const activeIndex = ref(0)
const search = ref<InstanceType<typeof AppSearchInput>>()

const groupOrder: readonly GatewayClientGroup[] = ['commandLine', 'desktop', 'web']

const selected = computed(
  () => gatewayClients.find((entry) => entry.id === props.modelValue) ?? gatewayClients[0]!,
)

function label(entry: GatewayClient): string {
  return t(`home.ledger.connection.clients.${entry.id}`)
}

/**
 * cc-switch 没有自己的固定协议（取决于目标应用），所以只对声明了
 * requiredProtocol 的客户端判定，不会误伤。
 */
function unsupported(entry: GatewayClient): boolean {
  return Boolean(entry.requiredProtocol && !props.protocols.includes(entry.requiredProtocol))
}

const matches = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase()
  if (needle === '') return [...gatewayClients]
  return gatewayClients.filter(
    (entry) =>
      label(entry).toLocaleLowerCase().includes(needle) ||
      entry.id.includes(needle) ||
      entry.searchTerms.some((term) => term.includes(needle)),
  )
})

/** 可用的按类型分组在前，此密钥用不了的统一沉到最后一组。 */
const sections = computed(() => {
  const available = matches.value.filter((entry) => !unsupported(entry))
  const blocked = matches.value.filter((entry) => unsupported(entry))
  const result: Array<{ key: string; title: string; entries: GatewayClient[] }> = []
  for (const group of groupOrder) {
    const entries = available.filter((entry) => clientGroup(entry.kind) === group)
    if (entries.length > 0) {
      result.push({ key: group, title: t(`home.ledger.connection.groups.${group}`), entries })
    }
  }
  if (blocked.length > 0) {
    result.push({
      key: 'unsupported',
      title: t('home.ledger.connection.groups.unsupported'),
      entries: blocked,
    })
  }
  return result
})

/** 拍平一份用于方向键导航，顺序与渲染顺序一致。 */
const flat = computed(() => sections.value.flatMap((section) => section.entries))

watch(flat, () => (activeIndex.value = 0))
watch(open, async (isOpen) => {
  if (!isOpen) {
    query.value = ''
    return
  }
  activeIndex.value = Math.max(
    flat.value.findIndex((entry) => entry.id === props.modelValue),
    0,
  )
  await nextTick()
  search.value?.focus()
})

function choose(entry: GatewayClient): void {
  if (props.disabled) return
  open.value = false
  if (entry.id !== props.modelValue) emit('update:modelValue', entry.id)
}

function onKeydown(event: KeyboardEvent): void {
  // AppSearchInput 把监听器转发到外层容器，清除按钮上的按键也会冒泡到这里。
  if (!(event.target instanceof HTMLInputElement)) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    activeIndex.value = Math.min(activeIndex.value + 1, flat.value.length - 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    activeIndex.value = Math.max(activeIndex.value - 1, 0)
  } else if (event.key === 'Enter') {
    const entry = flat.value[activeIndex.value]
    if (entry) {
      event.preventDefault()
      choose(entry)
    }
  }
}
</script>

<template>
  <AppPopover v-model:open="open" align="start" content-class="client-picker__panel">
    <template #trigger>
      <button
        class="client-picker__trigger"
        type="button"
        :disabled="disabled"
        :aria-expanded="open"
        :aria-controls="listID"
        :aria-label="t('home.ledger.connection.selectClient')"
      >
        <ChannelIcon :icon="selected.icon" :mark="selected.mark" />
        <span class="client-picker__current">{{ label(selected) }}</span>
        <ChevronDown :size="14" aria-hidden="true" />
      </button>
    </template>

    <AppSearchInput
      ref="search"
      v-model="query"
      :label="t('home.ledger.connection.searchClients')"
      :placeholder="t('home.ledger.connection.searchClientsPlaceholder')"
      :clear-label="t('home.ledger.connection.clearSearch')"
      @keydown="onKeydown"
    />

    <div :id="listID" class="client-picker__list">
      <template v-for="section in sections" :key="section.key">
        <p class="client-picker__group">{{ section.title }}</p>
        <button
          v-for="entry in section.entries"
          :key="entry.id"
          class="client-picker__option"
          :class="{
            'client-picker__option--active': flat[activeIndex]?.id === entry.id,
            'client-picker__option--selected': entry.id === modelValue,
            'client-picker__option--blocked': section.key === 'unsupported',
          }"
          type="button"
          :aria-current="entry.id === modelValue ? 'true' : undefined"
          @click="choose(entry)"
          @mouseenter="activeIndex = flat.findIndex((candidate) => candidate.id === entry.id)"
        >
          <ChannelIcon :icon="entry.icon" :mark="entry.mark" />
          <span class="client-picker__name">{{ label(entry) }}</span>
        </button>
      </template>

      <p v-if="sections.length === 0" class="client-picker__empty">
        {{ t('home.ledger.connection.noClientMatches') }}
      </p>
    </div>
  </AppPopover>
</template>

<style scoped>
.client-picker__trigger {
  display: inline-flex;
  min-width: 190px;
  min-height: var(--control-sm);
  align-items: center;
  gap: 8px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 10px;
  font: inherit;
  font-size: var(--text-button);
  font-weight: 560;
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.client-picker__trigger:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  background: var(--color-interactive-hover);
}

.client-picker__trigger:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

/*
 * 品牌 SVG 是 1em 见方，字母标兜底默认 min-width 1.7em——两者混排时宽度不同，
 * 名称就会一行左一行右。统一钉死图标槽宽度，所有名称严格对齐。
 */
.client-picker__trigger :deep(.channel-icon) {
  width: 18px;
  min-width: 18px;
  justify-content: center;
  font-size: 16px;
}

.client-picker__trigger :deep(.channel-icon--fallback) {
  width: 18px;
  min-width: 18px;
  height: 15px;
  font-size: 8px;
}

.client-picker__current {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.client-picker__trigger svg {
  flex: none;
  color: var(--color-text-faint);
}

.client-picker__list {
  display: grid;
  gap: 1px;
  margin-top: var(--space-3);
}

.client-picker__group {
  margin: 0;
  color: var(--color-text-faint);
  padding: 9px 7px 3px;
  font-size: var(--text-label-xs);
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}

.client-picker__group:first-child {
  padding-top: 2px;
}

.client-picker__option {
  display: grid;
  width: 100%;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  gap: var(--space-2);
  border: 0;
  border-radius: var(--radius-tag);
  background: transparent;
  color: var(--color-text);
  padding: 7px 8px;
  font: inherit;
  font-size: var(--text-meta);
  text-align: left;
  cursor: pointer;
}

.client-picker__option :deep(.channel-icon) {
  width: 20px;
  min-width: 20px;
  justify-content: center;
  font-size: 17px;
}

.client-picker__option :deep(.channel-icon--fallback) {
  width: 20px;
  min-width: 20px;
  height: 16px;
  font-size: 8.5px;
}

.client-picker__option--active {
  background: var(--color-interactive-hover);
}

.client-picker__option--selected {
  background: var(--color-surface-sunken);
  font-weight: 600;
}

/* 不用 disabled 拦：那样键盘和读屏都到不了，也看不到原因。 */
.client-picker__option--blocked {
  opacity: 0.5;
}

.client-picker__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.client-picker__empty {
  margin: 0;
  color: var(--color-text-faint);
  padding: 10px 8px;
  font-size: var(--text-meta);
}
</style>
