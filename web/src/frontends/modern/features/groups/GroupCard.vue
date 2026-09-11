<script setup lang="ts">
import { ChevronDown, Copy, KeyRound, Pencil, UserRound } from '@lucide/vue'
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { getGroupModelNames, isPaused, type GroupRow } from '@modern/api/groups'
import AppButton from '@modern/components/ui/AppButton.vue'
import AppIcon from '@modern/components/ui/AppIcon.vue'
import AppIconButton from '@modern/components/ui/AppIconButton.vue'
import AppSwitch from '@modern/components/ui/AppSwitch.vue'
import { useApiClient } from '@shared/http/client-context'

const props = defineProps<{
  group: GroupRow
  expanded: boolean
  pending: boolean
  enabledOverride?: boolean
}>()
defineEmits<{
  expand: []
  edit: [event: MouseEvent]
  toggle: [value: boolean]
  copy: [value: string]
}>()
const { t, n, locale } = useI18n()
const client = useApiClient()
const id = useId()
const allModels = ref(false)
const models = useQuery(
  computed(() => ({
    queryKey: ['modern', 'group-model-names', props.group.id],
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      getGroupModelNames(client, props.group.id, signal),
    enabled: props.expanded,
    staleTime: 30_000,
  })),
)
const visibleModels = computed(() =>
  (models.data.value ?? []).slice(0, allModels.value ? undefined : 30),
)
const activity = computed(() =>
  props.group.lastActiveHour === null
    ? t('groups.board.neverActive')
    : new Intl.DateTimeFormat(locale.value, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        hour12: false,
      }).format(props.group.lastActiveHour),
)
const endpointHost = computed(() => {
  try {
    return new URL(props.group.endpoint).host
  } catch {
    return props.group.endpoint
  }
})
const state = computed(() =>
  props.enabledOverride !== undefined ? 'syncing' : props.group.availability,
)
const paused = computed(() => isPaused(props.group))
const countDetails = computed(() => [
  { key: 'available', count: props.group.credentials.available },
  { key: 'cooldown', count: props.group.credentials.cooldown },
  { key: 'blacklisted', count: props.group.credentials.blacklisted },
  { key: 'disabled', count: props.group.credentials.disabled },
])
</script>

<template>
  <article
    class="modern-group-card"
    :class="{ 'is-expanded': expanded }"
    :data-state="state"
    :aria-labelledby="`${id}-title`"
  >
    <header class="modern-group-card-header">
      <span class="modern-group-channel-mark" aria-hidden="true">{{
        group.channelMark || group.channelName.slice(0, 2)
      }}</span>
      <div class="modern-group-card-identity">
        <h2 :id="`${id}-title`" :title="group.name">{{ group.name }}</h2>
        <div class="modern-group-card-subtitle">
          <span v-if="group.name.toLowerCase() !== group.channelName.toLowerCase()">{{
            group.channelName
          }}</span>
          <span
            ><AppIcon
              :icon="group.connectionType === 'subscription' ? UserRound : KeyRound"
              size="xs"
            />{{ t(`groups.connection.${group.connectionType}`) }}</span
          >
        </div>
      </div>
      <AppSwitch
        :model-value="enabledOverride ?? group.enabled"
        :label="t('groups.toggle', { name: group.name })"
        :loading="pending"
        @update:model-value="$emit('toggle', $event)"
      />
    </header>
    <div class="modern-group-service-line">
      <span class="modern-group-service-state"
        ><span aria-hidden="true" />{{ t(`groups.board.state.${state}`) }}</span
      >
      <span class="modern-group-price" :title="t('groups.board.price')"
        >{{ t('groups.edit.weight') }} {{ n(group.weight)
        }}<span>· {{ group.priceMultiplier }}×</span></span
      >
    </div>
    <div class="modern-group-card-metrics">
      <div>
        <span>{{ t('groups.board.credentials') }}</span
        ><strong>{{ n(group.credentials.total) }}</strong
        ><small v-if="!paused">{{
          t('groups.board.credentialsReady', { count: n(group.credentials.available) })
        }}</small>
      </div>
      <div>
        <span>{{ t('groups.columns.models') }}</span
        ><strong>{{ n(group.modelCount) }}</strong>
      </div>
    </div>
    <div class="modern-group-card-context">
      <p class="modern-group-model-preview" :title="group.modelPreview.join(' · ')">
        {{
          group.modelPreview.length ? group.modelPreview.join(' · ') : t('groups.board.noModels')
        }}
      </p>
      <div class="modern-group-activity">
        <span>{{ t('groups.board.activity') }}</span
        ><time
          v-if="group.lastActiveHour !== null"
          :datetime="new Date(group.lastActiveHour).toISOString()"
          :title="t('groups.board.activityHelp', { count: n(group.lastActiveHourRequests) })"
          >{{ activity }}</time
        ><span v-else>{{ activity }}</span>
      </div>
    </div>
    <div v-if="expanded" :id="`${id}-details`" class="modern-group-card-details">
      <section>
        <h3>{{ t('groups.board.connectionDetails') }}</h3>
        <div class="modern-group-full-endpoint">
          <code>{{ group.endpoint || t('groups.defaultEndpoint') }}</code
          ><AppIconButton
            v-if="group.endpoint"
            :icon="Copy"
            size="xs"
            :label="t('groups.copyURL')"
            @click="$emit('copy', group.endpoint)"
          />
        </div>
        <p class="modern-group-detail-note">{{ group.channelName }} · #{{ group.id }}</p>
      </section>
      <section>
        <h3>{{ t('groups.columns.credentials') }}</h3>
        <p v-if="paused" class="modern-group-detail-note">
          {{ t('groups.board.pausedCredentials') }}
        </p>
        <div v-else class="modern-group-credential-breakdown">
          <span v-for="item in countDetails" :key="item.key" :data-kind="item.key"
            >{{ t(`groups.credentials.${item.key}`) }}<strong>{{ n(item.count) }}</strong></span
          >
        </div>
        <p v-if="!paused && group.credentials.modelCooldown" class="modern-group-detail-note">
          {{ t('groups.modelCooldown', { count: n(group.credentials.modelCooldown) }) }}
        </p>
      </section>
      <section>
        <h3>{{ t('groups.board.modelDirectory') }}</h3>
        <p v-if="models.isPending.value" class="modern-group-detail-note" role="status">
          {{ t('collection.loading') }}
        </p>
        <div v-else-if="models.isError.value" class="modern-group-model-error">
          <span>{{ t('groups.board.modelsFailed') }}</span
          ><AppButton size="sm" @click="models.refetch()">{{ t('collection.retry') }}</AppButton>
        </div>
        <div v-else class="modern-group-model-chips">
          <span v-for="model in visibleModels" :key="model.id" :title="model.id">{{
            model.name
          }}</span
          ><span v-if="!visibleModels.length">{{ t('groups.board.noModels') }}</span>
        </div>
        <AppButton
          v-if="!allModels && (models.data.value?.length ?? 0) > 30"
          size="sm"
          variant="ghost"
          @click="allModels = true"
          >{{ t('groups.board.allModels', { count: n(models.data.value!.length) }) }}</AppButton
        >
      </section>
    </div>
    <footer class="modern-group-card-footer">
      <span class="modern-group-host" :title="group.endpoint">{{
        endpointHost || t('groups.defaultEndpoint')
      }}</span>
      <div class="modern-group-card-actions">
        <AppButton
          variant="ghost"
          size="sm"
          :icon="Pencil"
          :disabled="pending"
          @click="$emit('edit', $event)"
          >{{ t('groups.edit.action') }}</AppButton
        >
        <AppButton
          variant="ghost"
          size="sm"
          :aria-expanded="expanded"
          :aria-controls="`${id}-details`"
          @click="$emit('expand')"
          >{{ t(expanded ? 'groups.board.collapse' : 'groups.board.expand')
          }}<AppIcon :icon="ChevronDown" size="sm" :class="{ 'is-open': expanded }"
        /></AppButton>
      </div>
    </footer>
  </article>
</template>

<style scoped>
.modern-group-card {
  --modern-group-state-color: var(--modern-success);
  min-width: 0;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  background: var(--modern-surface);
}
.modern-group-card[data-state='limited'] {
  --modern-group-state-color: var(--modern-warning);
}
.modern-group-card[data-state='no_credentials'],
.modern-group-card[data-state='no_models'],
.modern-group-card[data-state='unavailable'] {
  --modern-group-state-color: var(--modern-danger);
}
.modern-group-card[data-state='paused'],
.modern-group-card[data-state='syncing'] {
  --modern-group-state-color: var(--modern-muted);
}
.modern-group-card.is-expanded {
  border-color: var(--modern-accent);
}
.modern-group-card-header {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  padding: var(--modern-space-5) var(--modern-space-5) var(--modern-space-4);
}
.modern-group-channel-mark {
  display: grid;
  width: var(--modern-control-nav);
  height: var(--modern-control-nav);
  flex-shrink: 0;
  place-items: center;
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-group-card-identity {
  flex: 1;
  min-width: 0;
}
.modern-group-card-identity h2 {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
}
.modern-group-card-subtitle {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  margin-top: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-group-card-subtitle span {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1);
}
.modern-group-service-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  padding: 0 var(--modern-space-5);
  font-size: var(--modern-font-size-small);
}
.modern-group-service-state {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-2);
  color: var(--modern-group-state-color);
}
.modern-group-service-state > span {
  width: var(--modern-space-1-5);
  height: var(--modern-space-1-5);
  border-radius: var(--modern-radius-round);
  background: currentColor;
}
.modern-group-price {
  color: var(--modern-text);
  font-variant-numeric: tabular-nums;
}
.modern-group-price > span {
  margin-left: var(--modern-space-1);
  color: var(--modern-muted);
}
.modern-group-card-metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  margin: var(--modern-space-4) var(--modern-space-5);
}
.modern-group-card-metrics > div {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--modern-space-2);
  padding-left: var(--modern-space-4);
  border-left: var(--modern-line-width) solid var(--modern-border);
}
.modern-group-card-metrics > div:first-child {
  padding-left: 0;
  border-left: 0;
}
.modern-group-card-metrics span,
.modern-group-card-metrics small {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-group-card-metrics strong {
  color: var(--modern-text);
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-medium);
  line-height: var(--modern-leading-title);
  font-variant-numeric: tabular-nums;
}
.modern-group-card-context {
  display: grid;
  gap: var(--modern-space-2);
  padding: 0 var(--modern-space-5) var(--modern-space-4);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-group-model-preview {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--modern-font-mono);
}
.modern-group-activity {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-group-card-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-2);
  padding: var(--modern-space-2) var(--modern-space-3) var(--modern-space-2) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-group-host {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-caption);
}
.modern-group-card-actions {
  display: flex;
  flex-shrink: 0;
  gap: var(--modern-space-1);
}
.modern-group-card-actions .is-open {
  transform: rotate(180deg);
}
.modern-group-card-details {
  display: grid;
  gap: var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding: var(--modern-space-5);
}
.modern-group-card-details h3 {
  margin-bottom: var(--modern-space-2);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
}
.modern-group-full-endpoint {
  display: flex;
  align-items: flex-start;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
}
.modern-group-full-endpoint code {
  flex: 1;
  min-width: 0;
  overflow-wrap: anywhere;
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-small);
}
.modern-group-detail-note {
  margin-top: var(--modern-space-2);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-credential-breakdown {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--modern-space-2);
}
.modern-group-credential-breakdown > span {
  display: grid;
  gap: var(--modern-space-1);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-group-credential-breakdown strong {
  font-weight: var(--modern-weight-medium);
  color: var(--modern-text);
}
.modern-group-credential-breakdown [data-kind='blacklisted'] strong {
  color: var(--modern-danger);
}
.modern-group-model-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-1-5);
}
.modern-group-model-chips > span {
  max-width: 100%;
  overflow-wrap: anywhere;
  border-radius: var(--modern-radius-small);
  background: var(--modern-subtle);
  padding: var(--modern-space-1) var(--modern-space-2);
  color: var(--modern-muted);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-caption);
}
.modern-group-model-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--modern-space-3);
  color: var(--modern-danger);
  font-size: var(--modern-font-size-small);
}
</style>
