<script setup lang="ts">
import { SlidersHorizontal, Stethoscope } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialRow } from '@modern/api/group-detail'
import {
  AppBadge,
  AppButton,
  AppCheckbox,
  AppCopyValue,
  AppIconButton,
  AppOverflowText,
  AppSwitch,
  AppTooltip,
} from '@modern/components/ui'
import { credentialStatus, credentialTime } from './credential-presentation'
import CredentialCardActions from './CredentialCardActions.vue'
import CredentialCardFrame from './CredentialCardFrame.vue'
import CredentialOutcomeSummary from './CredentialOutcomeSummary.vue'

const props = defineProps<{
  row: CredentialRow
  selected: boolean
  disabled: boolean
  pending?: boolean
  error?: string
  resolveSecret: () => Promise<string>
}>()
defineEmits<{ select: [value: boolean]; toggle: [value: boolean]; action: [value: string] }>()
const { t, n, locale } = useI18n()
const state = computed(() => credentialStatus(props.row))
const issues = computed(() =>
  [
    props.row.cooldownUntil
      ? t('groupDetail.recoversAt', { time: credentialTime(props.row.cooldownUntil, locale.value) })
      : '',
    props.row.failuresInRow
      ? t('credentialCards.consecutiveFailures', { count: n(props.row.failuresInRow) })
      : '',
    props.row.modelCooldowns.length
      ? t('credentialCards.modelCooldowns', { count: n(props.row.modelCooldowns.length) })
      : '',
  ]
    .filter(Boolean)
    .join(' · '),
)
</script>
<template>
  <CredentialCardFrame :selected="selected" :pending="pending" compact>
    <template #heading>
      <AppTooltip :label="t('groupDetail.selectCredential', { name: row.mask })"
        ><AppCheckbox
          :model-value="selected"
          :label="t('groupDetail.selectCredential', { name: row.mask })"
          label-hidden
          :disabled="disabled"
          @update:model-value="$emit('select', $event)"
      /></AppTooltip>
      <div class="modern-api-card-secret">
        <AppCopyValue
          :key="row.secretVersion"
          :value="row.mask"
          :resolve-value="resolveSecret"
          :label="t('credentialCards.copyKey')"
        />
      </div>
      <AppBadge :tone="state.tone" variant="plain" size="xs" dot
        ><AppOverflowText :text="t(state.key)"
      /></AppBadge>
    </template>
    <dl class="modern-api-card-metadata">
      <div>
        <dt>{{ t('groupDetail.lastUsed') }}</dt>
        <dd><AppOverflowText :text="credentialTime(row.lastUsed, locale)" /></dd>
      </div>
      <div>
        <dt>{{ t('groups.edit.weight') }}</dt>
        <dd>
          {{ n(row.weight)
          }}<AppIconButton
            :icon="SlidersHorizontal"
            :label="t('credentialCards.diagnosticsAndSettings')"
            size="xs"
            :disabled="disabled"
            @click="$emit('action', 'details')"
          />
        </dd>
      </div>
    </dl>
    <div
      class="modern-api-card-issues"
      :class="{ 'has-error': error }"
      :role="error ? 'alert' : undefined"
    >
      <AppOverflowText v-if="error" :text="error" />
      <AppButton
        v-else-if="issues"
        variant="text"
        size="xs"
        :disabled="disabled"
        @click="$emit('action', 'details')"
        ><AppOverflowText :text="issues"
      /></AppButton>
    </div>
    <template #footer
      ><CredentialOutcomeSummary :usage="row.daily" />
      <div class="modern-api-card-actions">
        <AppIconButton
          :icon="Stethoscope"
          :label="t('credentialCards.test')"
          size="xs"
          :disabled="disabled"
          @click="$emit('action', 'test')"
        /><CredentialCardActions
          :row="row"
          :disabled="disabled"
          @action="$emit('action', $event)"
        /><AppSwitch
          :model-value="row.enabled"
          :label="t('groups.edit.enabled')"
          :disabled="disabled"
          @update:model-value="$emit('toggle', $event)"
        /></div
    ></template>
  </CredentialCardFrame>
</template>
<style scoped>
.modern-api-card-secret {
  flex: 1;
  min-width: 0;
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-secondary);
}
.modern-api-card-metadata {
  display: grid;
  grid-template-columns: minmax(0, 1.7fr) minmax(0, 1fr);
  gap: var(--modern-space-3);
  margin: 0;
}
.modern-api-card-metadata > div {
  min-width: 0;
}
.modern-api-card-metadata dt {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-api-card-metadata dd {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  min-height: var(--modern-control-xs);
  margin: var(--modern-space-0-5) 0 0;
  font-size: var(--modern-font-size-small);
  font-variant-numeric: tabular-nums;
}
.modern-api-card-issues {
  display: flex;
  align-items: center;
  min-width: 0;
  min-height: var(--modern-control-xs);
  color: var(--modern-warning);
  font-size: var(--modern-font-size-small);
}
.modern-api-card-issues.has-error {
  color: var(--modern-danger);
}
.modern-api-card-issues > button {
  min-width: 0;
  max-width: 100%;
  color: inherit;
}
.modern-api-card-actions {
  display: flex;
  align-items: center;
  flex: none;
  gap: var(--modern-space-1);
  margin-left: auto;
}
</style>
