<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GroupProtocol } from '@/api/control/types'
import { enabledDataProtocols } from '@/api/control/protocols'
import UpstreamBaseURLHint from '@/components/config/UpstreamBaseURLHint.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'

const props = defineProps<{
  section: 'general' | 'routing'
  name: string
  upstreamUrl: string
  validationModel: string | null
  weightManual: number | null
  protocols: GroupProtocol[]
  enabled: boolean
  pending: boolean
  nameError: string
  upstreamUrlError: string
  protocolsError: string
}>()
const emit = defineEmits<{
  'update:name': [value: string]
  'update:upstreamUrl': [value: string]
  'update:validationModel': [value: string | null]
  'update:weightManual': [value: number | null]
  toggleProtocol: [protocol: GroupProtocol, checked: boolean]
  'update:enabled': [value: boolean]
}>()
const { t } = useI18n()
const weightMode = computed(() => (props.weightManual === null ? 'auto' : 'manual'))
const weightModes = computed(() => [
  { value: 'auto', label: t('group.settings.base.auto'), disabled: props.pending },
  { value: 'manual', label: t('group.settings.base.manual'), disabled: props.pending },
])
const weightValid = computed(
  () =>
    props.weightManual === null ||
    (Number.isInteger(props.weightManual) && props.weightManual >= 1 && props.weightManual <= 100),
)

function setWeightMode(value: string): void {
  if (props.pending) return
  emit('update:weightManual', value === 'auto' ? null : (props.weightManual ?? 50))
}
</script>

<template>
  <section v-if="section === 'general'" id="settings-general" class="group-settings__section">
    <header class="group-settings__section-heading">
      <h3>{{ t('group.settings.sections.general') }}</h3>
      <p>{{ t('group.settings.base.description') }}</p>
    </header>
    <div class="group-settings__grid">
      <label class="group-settings__field">
        <span>{{ t('group.settings.base.name') }}</span>
        <input
          :value="name"
          :disabled="pending"
          :aria-invalid="nameError ? 'true' : undefined"
          @input="emit('update:name', ($event.target as HTMLInputElement).value)"
        />
        <small v-if="nameError" role="alert">{{ nameError }}</small>
      </label>
      <label class="group-settings__field">
        <span>{{ t('group.settings.base.validationModel') }}</span>
        <input
          class="group-settings__mono"
          :value="validationModel ?? ''"
          :disabled="pending"
          @input="emit('update:validationModel', ($event.target as HTMLInputElement).value || null)"
        />
      </label>
      <label class="group-settings__field group-settings__wide">
        <span>{{ t('group.settings.base.upstreamUrl') }}</span>
        <input
          class="group-settings__mono"
          type="url"
          :value="upstreamUrl"
          :disabled="pending"
          :aria-invalid="upstreamUrlError ? 'true' : undefined"
          @input="emit('update:upstreamUrl', ($event.target as HTMLInputElement).value)"
        />
        <small v-if="upstreamUrlError" role="alert">{{ upstreamUrlError }}</small>
        <small>{{ t('group.settings.base.urlWarning') }}</small>
        <UpstreamBaseURLHint
          :url="upstreamUrl"
          :protocols="protocols"
          :missing-message="t('group.settings.base.urlPrefixMissing')"
          :duplicate-message="t('group.settings.base.urlPrefixDuplicate')"
        />
      </label>
    </div>
    <label class="group-settings__switch-row">
      <span class="group-settings__switch-copy">
        <strong>{{ t('group.settings.base.enabled') }}</strong>
        <small>{{ t('group.settings.base.enabledHelp') }}</small>
      </span>
      <span class="group-settings__switch">
        <input
          type="checkbox"
          :checked="enabled"
          :disabled="pending"
          @change="emit('update:enabled', ($event.target as HTMLInputElement).checked)"
        />
        <span aria-hidden="true"></span>
      </span>
    </label>
  </section>

  <section v-else id="settings-routing" class="group-settings__section">
    <header class="group-settings__section-heading">
      <h3>{{ t('group.settings.sections.routing') }}</h3>
      <p>{{ t('group.settings.routing.description') }}</p>
    </header>
    <fieldset class="group-settings__field group-settings__wide">
      <legend>{{ t('group.settings.base.protocols') }}</legend>
      <div class="group-settings__checks">
        <label v-for="protocol in enabledDataProtocols" :key="protocol">
          <input
            type="checkbox"
            :checked="protocols.includes(protocol)"
            :disabled="pending"
            @change="emit('toggleProtocol', protocol, ($event.target as HTMLInputElement).checked)"
          />
          <span class="group-settings__check-box" aria-hidden="true">
            <Check :size="14" />
          </span>
          <code>{{ protocol }}</code>
        </label>
      </div>
      <small v-if="protocolsError" role="alert">{{ protocolsError }}</small>
    </fieldset>
    <div class="group-settings__field group-settings__wide">
      <span>{{ t('group.settings.base.weight') }}</span>
      <div class="group-settings__weight-editor">
        <SegmentedControl
          :model-value="weightMode"
          :label="t('group.settings.base.weight')"
          :options="weightModes"
          size="compact"
          @update:model-value="setWeightMode"
        />
        <input
          v-if="weightManual !== null"
          class="group-settings__mono"
          type="number"
          min="1"
          max="100"
          step="1"
          inputmode="numeric"
          :value="weightManual"
          :disabled="pending"
          :aria-label="t('group.settings.base.weight')"
          :aria-invalid="!weightValid || undefined"
          @input="emit('update:weightManual', Number(($event.target as HTMLInputElement).value))"
        />
      </div>
      <small>{{ t('group.settings.routing.weightHelp') }}</small>
      <small v-if="!weightValid" role="alert">{{ t('group.settings.base.weightError') }}</small>
    </div>
  </section>
</template>

<style scoped>
.group-settings__section {
  display: grid;
  gap: 15px;
  scroll-margin-top: 76px;
  border-top: 1px solid var(--color-border-control);
  padding-top: 17px;
}

.group-settings__section:first-child {
  border-top: 0;
  padding-top: 0;
}

.group-settings__section-heading h3,
.group-settings__section-heading p {
  margin: 0;
}

.group-settings__section-heading h3 {
  font-size: 14px;
  font-weight: 650;
}

.group-settings__section-heading p {
  max-width: 580px;
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: var(--text-sm);
}

.group-settings__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 15px 18px;
}

.group-settings__wide {
  grid-column: 1 / -1;
}

.group-settings__field {
  display: grid;
  align-content: start;
  gap: 6px;
}

.group-settings__field > span,
.group-settings__field > legend {
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  font-weight: 560;
}

.group-settings__field small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: var(--line-normal);
}

.group-settings__field small[role='alert'] {
  color: var(--color-danger);
}

.group-settings__field input:not([type='checkbox']) {
  width: 100%;
  min-height: var(--control-md);
  border: 1px solid var(--color-border-control);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  color: var(--color-text);
  padding: 0 var(--space-3);
  font: inherit;
}

.group-settings__mono,
.group-settings__field code {
  font-family: var(--font-mono);
}

fieldset {
  margin: 0;
  border: 0;
  padding: 0;
}

.group-settings__switch-row {
  display: flex;
  min-height: 48px;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
  border-top: 1px solid var(--color-border-subtle);
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 8px 2px;
}

.group-settings__switch-copy {
  display: grid;
}

.group-settings__switch-copy strong {
  font-size: 12.5px;
}

.group-settings__switch-copy small {
  color: var(--color-text-faint);
  font-size: 11px;
}

.group-settings__switch {
  position: relative;
  display: inline-flex;
  width: 42px;
  height: 24px;
  flex: none;
  cursor: pointer;
}

.group-settings__switch input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}

.group-settings__switch > span {
  width: 100%;
  border: 1px solid var(--color-border-control);
  border-radius: 999px;
  background: var(--color-surface-sunken);
  transition:
    background-color var(--duration-fast) var(--easing-standard),
    border-color var(--duration-fast) var(--easing-standard);
}

.group-settings__switch > span::after {
  position: absolute;
  top: 4px;
  left: 4px;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--color-text-faint);
  content: '';
  transition:
    transform var(--duration-fast) var(--easing-standard),
    background-color var(--duration-fast) var(--easing-standard);
}

.group-settings__switch input:checked + span {
  border-color: var(--color-action);
  background: var(--color-action);
}

.group-settings__switch input:checked + span::after {
  transform: translateX(18px);
  background: var(--color-action-ink);
}

.group-settings__switch input:disabled + span,
.group-settings__checks input:disabled + .group-settings__check-box {
  cursor: not-allowed;
  opacity: 0.55;
}

.group-settings__checks {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
}

.group-settings__checks label {
  position: relative;
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr);
  min-height: var(--touch-target);
  align-items: center;
  gap: 9px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface);
  padding: 7px 10px;
  cursor: pointer;
}

.group-settings__checks label:hover {
  border-color: var(--color-border-control);
}

.group-settings__checks input {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  overflow: hidden;
  clip-path: inset(50%);
  opacity: 0;
}

.group-settings__check-box {
  display: grid;
  width: 18px;
  height: 18px;
  place-items: center;
  border: 1px solid var(--color-border-control);
  border-radius: 4px;
  background: var(--color-surface);
  color: var(--color-action-ink);
}

.group-settings__check-box svg {
  opacity: 0;
  transform: scale(0.78);
  transition:
    opacity var(--duration-fast) var(--easing-standard),
    transform var(--duration-fast) var(--easing-standard);
}

.group-settings__checks input:checked + .group-settings__check-box {
  border-color: var(--color-action);
  background: var(--color-action);
}

.group-settings__checks input:checked + .group-settings__check-box svg {
  opacity: 1;
  transform: scale(1);
}

.group-settings__checks code {
  overflow-wrap: anywhere;
  font-size: var(--text-label-xs);
}

.group-settings__weight-editor {
  display: flex;
  align-items: center;
  gap: 10px;
}

.group-settings__field .group-settings__weight-editor > input {
  width: 90px !important;
  min-height: var(--control-compact);
  flex: 0 0 90px;
}

@media (max-width: 800px) {
  .group-settings__grid,
  .group-settings__checks {
    grid-template-columns: 1fr;
  }

  .group-settings__wide {
    grid-column: auto;
  }

  .group-settings__field input:not([type='checkbox']) {
    min-height: var(--touch-target);
    font-size: 16px;
  }

  .group-settings__weight-editor :deep(.segmented-control__trigger) {
    min-height: var(--touch-target);
  }

  .group-settings__field .group-settings__weight-editor > input {
    min-height: var(--touch-target);
  }

  .group-settings__switch {
    width: var(--touch-target);
    height: var(--touch-target);
  }

  .group-settings__switch > span {
    position: absolute;
    top: 10px;
    right: 0;
    width: 42px;
    height: 24px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .group-settings__switch > span,
  .group-settings__switch > span::after,
  .group-settings__check-box svg {
    transition: none;
  }
}
</style>
