<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ChannelConnectionType, ChannelDto } from '@/app/resources/channels'
import AppButton from '@/components/ui/AppButton.vue'
import AppPopover from '@/components/ui/AppPopover.vue'
import AppSearchInput from '@/components/ui/AppSearchInput.vue'
import AsyncRefreshIndicator from '@/components/ui/AsyncRefreshIndicator.vue'
import InlineFeedback from '@/components/ui/InlineFeedback.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'
import SegmentedControl, { type SegmentedControlOption } from '@/components/ui/SegmentedControl.vue'
import ChannelIcon from '@/components/brand/ChannelIcon.vue'

const MAX_FEATURED_CHANNELS = 4
const FEATURED_CHANNEL_IDS: Record<ChannelConnectionType, readonly string[]> = {
  api_key: ['openai', 'anthropic', 'gemini', 'openai_compatible'],
  subscription: ['codex', 'claude'],
}

const props = withDefaults(
  defineProps<{
    modelValue: string | null
    channels: readonly ChannelDto[]
    selectedChannel: ChannelDto | null
    loading: boolean
    error: boolean
    disabled?: boolean
    hideHeader?: boolean
    compact?: boolean
  }>(),
  {
    disabled: false,
    hideHeader: false,
    compact: false,
  },
)
const emit = defineEmits<{
  select: [channel: ChannelDto]
  retry: []
}>()
const { t } = useI18n()

interface ChannelMatch {
  channel: ChannelDto
  rank: number
  reason: string
}

interface HighlightSegment {
  text: string
  matched: boolean
}

const popoverOpen = ref(false)
const query = ref('')
const activeIndex = ref(0)
const searchInput = ref<InstanceType<typeof AppSearchInput>>()
const identity = useId()
const optionId = (channelID: string) => `${identity}-channel-${channelID}`
const channelListId = `${identity}-channel-list`
const activeConnectionType = ref<ChannelConnectionType>(
  props.selectedChannel?.connection.type ?? 'api_key',
)
const lastSelectedChannelIDs = ref<Partial<Record<ChannelConnectionType, string>>>({})

const initialLoading = computed(() => props.loading && props.channels.length === 0)
const loadFailed = computed(() => props.error && props.channels.length === 0)

function channelsForType(type: ChannelConnectionType): ChannelDto[] {
  return props.channels.filter((channel) => channel.connection.type === type)
}

function featuredChannelsForType(type: ChannelConnectionType): ChannelDto[] {
  const channels = channelsForType(type)
  const byID = new Map(channels.map((channel) => [channel.channel_id, channel]))
  const preferredIDs = new Set(FEATURED_CHANNEL_IDS[type])
  return [
    ...FEATURED_CHANNEL_IDS[type]
      .map((channelID) => byID.get(channelID))
      .filter((channel): channel is ChannelDto => channel !== undefined),
    ...channels.filter((channel) => !preferredIDs.has(channel.channel_id)),
  ].slice(0, MAX_FEATURED_CHANNELS)
}

const connectionTypeOptions = computed<SegmentedControlOption[]>(() => [
  {
    value: 'api_key',
    label: t('import.steps.channel.connectionTypes.apiKey'),
    disabled: props.disabled || channelsForType('api_key').length === 0,
  },
  {
    value: 'subscription',
    label: t('import.steps.channel.connectionTypes.subscription'),
    disabled: props.disabled || channelsForType('subscription').length === 0,
  },
])
const activeChannels = computed(() => channelsForType(activeConnectionType.value))
const featuredChannels = computed(() => featuredChannelsForType(activeConnectionType.value))
const featuredChannelIDs = computed(
  () => new Set(featuredChannels.value.map((channel) => channel.channel_id)),
)
const extraChannel = computed(() =>
  props.selectedChannel?.connection.type === activeConnectionType.value &&
  !featuredChannelIDs.value.has(props.selectedChannel.channel_id)
    ? props.selectedChannel
    : null,
)
const otherChannels = computed(() =>
  activeChannels.value.filter((channel) => !featuredChannelIDs.value.has(channel.channel_id)),
)

watch(
  () => props.selectedChannel,
  (channel) => {
    if (!channel) return
    activeConnectionType.value = channel.connection.type
    lastSelectedChannelIDs.value = {
      ...lastSelectedChannelIDs.value,
      [channel.connection.type]: channel.channel_id,
    }
  },
  { immediate: true },
)

function matchChannel(channel: ChannelDto, normalizedQuery: string): ChannelMatch | null {
  const name = channel.name.toLocaleLowerCase()
  if (name.startsWith(normalizedQuery)) return { channel, rank: 100, reason: '' }
  if (name.includes(normalizedQuery)) return { channel, rank: 80, reason: '' }
  const id = channel.channel_id
  if (id.startsWith(normalizedQuery)) return { channel, rank: 70, reason: id }
  if (id.includes(normalizedQuery)) return { channel, rank: 60, reason: id }
  for (const term of channel.search_terms) {
    const lower = term.toLocaleLowerCase()
    if (lower.startsWith(normalizedQuery)) return { channel, rank: 50, reason: term }
    if (lower.includes(normalizedQuery)) return { channel, rank: 30, reason: term }
  }
  // The server-side search this replaced also matched descriptions, which is
  // the only way "microsoft" reaches Azure OpenAI. Rank it last so it never
  // outranks a name or alias hit.
  if (channel.description.toLocaleLowerCase().includes(normalizedQuery)) {
    return { channel, rank: 10, reason: channel.description }
  }
  return null
}

const rankedMatches = computed<ChannelMatch[]>(() => {
  const normalizedQuery = query.value.trim().toLocaleLowerCase()
  if (!normalizedQuery) {
    return otherChannels.value.map((channel) => ({ channel, rank: 0, reason: '' }))
  }
  return activeChannels.value
    .map((channel, index) => ({ index, match: matchChannel(channel, normalizedQuery) }))
    .filter((row): row is { index: number; match: ChannelMatch } => row.match !== null)
    .sort((a, b) => b.match.rank - a.match.rank || a.index - b.index)
    .map((row) => row.match)
})

watch(rankedMatches, () => {
  activeIndex.value = 0
})

const activeOptionId = computed(() => {
  const match = rankedMatches.value[activeIndex.value]
  return match ? optionId(match.channel.channel_id) : undefined
})

// Focus stays in the search input, so the highlighted option has to be scrolled
// into view explicitly — otherwise Enter can select a row below the fold.
watch(activeOptionId, async (id) => {
  if (!id) return
  await nextTick()
  document.getElementById(id)?.scrollIntoView({ block: 'nearest' })
})

watch(popoverOpen, async (open) => {
  if (!open) return
  query.value = ''
  activeIndex.value = 0
  await nextTick()
  searchInput.value?.focus()
})

function highlightSegments(text: string, rawQuery: string): HighlightSegment[] {
  const trimmed = rawQuery.trim()
  if (!trimmed) return [{ text, matched: false }]
  const at = text.toLocaleLowerCase().indexOf(trimmed.toLocaleLowerCase())
  if (at < 0) return [{ text, matched: false }]
  const segments: HighlightSegment[] = []
  if (at > 0) segments.push({ text: text.slice(0, at), matched: false })
  segments.push({ text: text.slice(at, at + trimmed.length), matched: true })
  if (at + trimmed.length < text.length) {
    segments.push({ text: text.slice(at + trimmed.length), matched: false })
  }
  return segments
}

function channelSelected(channel: ChannelDto): boolean {
  return props.modelValue === channel.channel_id
}

function choose(channel: ChannelDto): void {
  if (props.disabled) return
  activeConnectionType.value = channel.connection.type
  lastSelectedChannelIDs.value = {
    ...lastSelectedChannelIDs.value,
    [channel.connection.type]: channel.channel_id,
  }
  popoverOpen.value = false
  emit('select', channel)
}

function chooseConnectionType(value: string): void {
  if (value !== 'api_key' && value !== 'subscription') return
  const type: ChannelConnectionType = value
  if (props.disabled || type === activeConnectionType.value) return
  activeConnectionType.value = type
  popoverOpen.value = false

  const channels = channelsForType(type)
  const rememberedID = lastSelectedChannelIDs.value[type]
  const target =
    channels.find((channel) => channel.channel_id === rememberedID) ??
    featuredChannelsForType(type)[0] ??
    channels[0]
  if (target) choose(target)
}

function onSearchKeydown(event: KeyboardEvent): void {
  // AppSearchInput forwards listeners to its wrapper element, so keys pressed
  // on the clear button bubble here too. Without this guard, Enter on that
  // button would select the highlighted channel instead of clearing the query.
  if (!(event.target instanceof HTMLInputElement)) return
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    activeIndex.value = Math.min(activeIndex.value + 1, rankedMatches.value.length - 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    activeIndex.value = Math.max(activeIndex.value - 1, 0)
  } else if (event.key === 'Enter') {
    const match = rankedMatches.value[activeIndex.value]
    if (match) {
      event.preventDefault()
      choose(match.channel)
    }
  }
}
</script>

<template>
  <section
    class="channel-picker"
    :class="{ 'channel-picker--compact': compact }"
    :aria-labelledby="hideHeader ? undefined : 'channel-picker-heading'"
    :aria-label="hideHeader ? t('import.presets.title') : undefined"
  >
    <div v-if="!hideHeader" class="channel-picker__header">
      <h2 id="channel-picker-heading">{{ t('import.presets.title') }}</h2>
      <span v-if="selectedChannel" class="channel-picker__current">
        <span class="channel-picker__current-label">{{ t('import.presets.current') }}</span>
        <ChannelIcon :icon="selectedChannel.icon" :mark="selectedChannel.mark" />
        <span class="channel-picker__current-name">{{ selectedChannel.name }}</span>
      </span>
    </div>

    <div class="channel-picker__row">
      <AsyncRefreshIndicator
        :active="loading && channels.length > 0"
        :label="t('import.presets.loading')"
      />

      <InlineFeedback v-if="initialLoading" tone="neutral">
        {{ t('import.presets.loading') }}
      </InlineFeedback>
      <InlineFeedback v-else-if="loadFailed" tone="danger">
        {{ t('import.presets.loadFailed') }}
        <template #action>
          <AppButton variant="link" size="inline" @click="emit('retry')">
            {{ t('common.retry') }}
          </AppButton>
        </template>
      </InlineFeedback>

      <div v-else class="channel-picker__selector">
        <SegmentedControl
          :model-value="activeConnectionType"
          :label="t('import.presets.connectionType')"
          :options="connectionTypeOptions"
          :controls-id="channelListId"
          appearance="drawer"
          size="sm"
          @update:model-value="chooseConnectionType"
        />

        <span class="channel-picker__divider" aria-hidden="true" />

        <div :id="channelListId" class="channel-picker__chips">
          <button
            v-for="channel in featuredChannels"
            :key="channel.channel_id"
            type="button"
            class="channel-picker__chip"
            :class="{
              'channel-picker__chip--subscription': activeConnectionType === 'subscription',
              'channel-picker__chip--selected': channelSelected(channel),
            }"
            :disabled="disabled"
            :aria-pressed="channelSelected(channel)"
            @click="choose(channel)"
          >
            <ChannelIcon :icon="channel.icon" :mark="channel.mark" />
            <span>{{ channel.name }}</span>
          </button>

          <span
            v-if="activeConnectionType === 'api_key'"
            class="channel-picker__divider"
            aria-hidden="true"
          />

          <AppPopover
            v-if="activeConnectionType === 'api_key'"
            v-model:open="popoverOpen"
            align="start"
            content-class="channel-picker__panel"
          >
            <template #trigger>
              <button
                type="button"
                class="channel-picker__chip channel-picker__chip--more"
                :class="{ 'channel-picker__chip--selected': extraChannel !== null }"
                :disabled="disabled"
                :aria-expanded="popoverOpen"
                :aria-pressed="extraChannel !== null"
              >
                <ChannelIcon
                  v-if="extraChannel"
                  :icon="extraChannel.icon"
                  :mark="extraChannel.mark"
                />
                <span>{{ extraChannel?.name ?? t('import.presets.more') }}</span>
                <ChevronDown
                  class="channel-picker__caret"
                  :class="{ 'channel-picker__caret--open': popoverOpen }"
                  :size="13"
                  aria-hidden="true"
                />
              </button>
            </template>

            <div class="channel-picker__panel-inner">
              <AppSearchInput
                ref="searchInput"
                v-model="query"
                :label="t('import.presets.search')"
                :placeholder="t('import.presets.search')"
                :clear-label="t('import.presets.clearSearch')"
                :active-descendant="activeOptionId"
                @keydown="onSearchKeydown"
              />

              <InlineFeedback v-if="rankedMatches.length === 0" tone="neutral" appearance="hint">
                {{ t('import.presets.noMatches') }}
              </InlineFeedback>
              <div
                v-else
                class="channel-picker__options"
                role="listbox"
                :aria-label="t('import.presets.more')"
              >
                <button
                  v-for="(match, index) in rankedMatches"
                  :id="optionId(match.channel.channel_id)"
                  :key="match.channel.channel_id"
                  type="button"
                  role="option"
                  class="channel-picker__option"
                  :class="{ 'channel-picker__option--active': index === activeIndex }"
                  :aria-selected="channelSelected(match.channel)"
                  @click="choose(match.channel)"
                  @mouseenter="activeIndex = index"
                >
                  <ChannelIcon :icon="match.channel.icon" :mark="match.channel.mark" />
                  <OverflowTooltip
                    as="span"
                    class="channel-picker__option-name"
                    :content="match.channel.name"
                    :focusable="false"
                  >
                    <template
                      v-for="(segment, segIndex) in highlightSegments(match.channel.name, query)"
                      :key="segIndex"
                    >
                      <mark v-if="segment.matched">{{ segment.text }}</mark>
                      <template v-else>{{ segment.text }}</template>
                    </template>
                  </OverflowTooltip>
                  <span v-if="match.reason" class="channel-picker__option-reason">
                    <template
                      v-for="(segment, segIndex) in highlightSegments(match.reason, query)"
                      :key="segIndex"
                    >
                      <mark v-if="segment.matched">{{ segment.text }}</mark>
                      <template v-else>{{ segment.text }}</template>
                    </template>
                  </span>
                </button>
              </div>
            </div>
          </AppPopover>
        </div>
      </div>
    </div>

    <InlineFeedback
      v-if="!initialLoading && !loadFailed && activeConnectionType === 'subscription'"
      class="channel-picker__subscription-risk"
      tone="warning"
      appearance="ledger"
    >
      {{ t('import.presets.subscriptionRisk') }}
    </InlineFeedback>
  </section>
</template>

<style scoped>
.channel-picker {
  min-width: 0;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 22px 0 var(--space-6);
}

.channel-picker--compact {
  border-bottom: 0;
  padding: 0;
}

.channel-picker__header {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.channel-picker__header h2 {
  margin: 0;
  font-size: var(--title-section);
  font-weight: 650;
  letter-spacing: -0.01em;
}

.channel-picker__current {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.channel-picker__current-label {
  color: var(--color-text-faint);
}

.channel-picker__current-name {
  color: var(--color-text-muted);
  font-weight: 560;
}

.channel-picker__row {
  position: relative;
}

.channel-picker__selector {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.channel-picker__chips {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.channel-picker__chip {
  display: inline-flex;
  min-height: var(--control-sm);
  align-items: center;
  gap: 8px;
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 13px;
  font: inherit;
  font-size: var(--text-button);
  font-weight: 560;
  white-space: nowrap;
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard),
    color var(--duration-fast) var(--easing-standard);
}

.channel-picker__chip:hover:not(:disabled) {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}

.channel-picker__chip--subscription {
  border-color: color-mix(in srgb, var(--color-action) 24%, var(--color-border-subtle));
  background: color-mix(in srgb, var(--color-action-soft) 46%, var(--color-surface));
}

.channel-picker__chip--subscription:hover:not(:disabled) {
  border-color: color-mix(in srgb, var(--color-action) 42%, var(--color-border-control));
  background: color-mix(in srgb, var(--color-action-soft) 72%, var(--color-surface));
}

.channel-picker__chip--subscription:not(.channel-picker__chip--selected) {
  color: var(--color-text-muted);
}

.channel-picker__chip:disabled {
  cursor: not-allowed;
  opacity: 0.55;
}

.channel-picker__chip--selected {
  border-color: var(--color-action);
  background: var(--color-action-soft);
  color: var(--color-action);
  font-weight: 620;
}

.channel-picker__chip--more .channel-picker__caret {
  color: var(--color-text-faint);
  transition: transform var(--duration-fast) var(--easing-standard);
}

.channel-picker__chip--more[aria-expanded='true'] {
  border-color: var(--color-text-faint);
  background: var(--color-surface-sunken);
}

.channel-picker__chip--more.channel-picker__chip--subscription[aria-expanded='true'] {
  border-color: color-mix(in srgb, var(--color-action) 42%, var(--color-border-control));
  background: color-mix(in srgb, var(--color-action-soft) 72%, var(--color-surface));
}

.channel-picker__chip--more.channel-picker__chip--selected,
.channel-picker__chip--more.channel-picker__chip--selected[aria-expanded='true'] {
  border-color: var(--color-action);
  background: var(--color-action-soft);
  color: var(--color-action);
}

.channel-picker__chip--subscription.channel-picker__chip--selected,
.channel-picker__chip--subscription.channel-picker__chip--selected[aria-expanded='true'] {
  border-color: var(--color-action);
  background: var(--color-action);
  color: var(--color-action-ink);
  font-weight: 650;
}

.channel-picker__chip--subscription.channel-picker__chip--selected:hover:not(:disabled) {
  border-color: var(--color-action-hover);
  background: var(--color-action-hover);
}

.channel-picker__chip--subscription.channel-picker__chip--selected .channel-picker__caret {
  color: currentColor;
}

.channel-picker__caret--open {
  transform: rotate(180deg);
}

.channel-picker__divider {
  width: 1px;
  height: 20px;
  flex: none;
  background: var(--color-border-subtle);
}

.channel-picker__panel-inner {
  display: flex;
  width: min(360px, 100%);
  flex-direction: column;
  gap: var(--space-3);
}

.channel-picker__options {
  display: grid;
  max-height: 320px;
  gap: 2px;
  overflow-y: auto;
}

.channel-picker__option {
  display: grid;
  min-width: 0;
  min-height: var(--control-sm);
  grid-template-columns: 20px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--color-text);
  padding: 7px 8px;
  font: inherit;
  font-size: var(--text-body);
  text-align: left;
  cursor: pointer;
}

.channel-picker__option:hover,
.channel-picker__option--active {
  background: var(--color-surface-sunken);
}

.channel-picker__option[aria-selected='true'] .channel-picker__option-name {
  color: var(--color-action);
  font-weight: 600;
}

.channel-picker__option-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.channel-picker__option-name mark,
.channel-picker__option-reason mark {
  border-radius: 2px;
  background: color-mix(in srgb, var(--color-warning) 26%, transparent);
  color: inherit;
  font-weight: 680;
}

.channel-picker__option-reason {
  max-width: 45%;
  flex: none;
  overflow: hidden;
  border-radius: 4px;
  background: var(--color-surface-sunken);
  padding: 2px 6px;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 10.5px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.channel-picker__subscription-risk {
  margin-top: var(--space-3);
}

@media (max-width: 860px) {
  .channel-picker__selector {
    align-items: flex-start;
  }

  .channel-picker__selector > .channel-picker__divider {
    display: none;
  }

  .channel-picker__chips {
    flex-basis: 100%;
  }

  .channel-picker__chip {
    min-height: var(--touch-target);
  }

  .channel-picker__chip--more {
    flex: 1;
    justify-content: center;
  }
}
</style>
