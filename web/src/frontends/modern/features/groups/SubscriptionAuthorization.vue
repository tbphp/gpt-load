<script setup lang="ts">
import { ExternalLink, Send } from '@lucide/vue'
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CredentialStage } from '@modern/api/credential-stages'
import {
  AppButton,
  AppCopyValue,
  AppExternalLink,
  AppIcon,
  AppTextArea,
} from '@modern/components/ui'
import { copyText } from '@modern/components/ui/clipboard'

const props = defineProps<{
  stage: CredentialStage
  modelValue: string
  error?: string
  disabled?: boolean
  submitting: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  submit: []
  restart: []
}>()
const { t } = useI18n()
const copying = ref(false)
const copyState = ref<'idle' | 'success' | 'manual'>('idle')
const manualCopy = ref<InstanceType<typeof AppTextArea>>()
let copyTimer: ReturnType<typeof setTimeout> | undefined
let copyRevision = 0
let disposed = false
function resetCopy(): void {
  copyRevision++
  clearTimeout(copyTimer)
  copying.value = false
  copyState.value = 'idle'
}
async function copyAuthorizationURL(): Promise<void> {
  const value = props.stage.authorizationURL
  if (!value || copying.value || props.disabled) return
  resetCopy()
  const revision = copyRevision
  const isCurrent = () => !disposed && copyRevision === revision
  copying.value = true
  let copied = false
  try {
    copied = await copyText(value, undefined, isCurrent)
  } catch {
    // 浏览器拒绝两种复制方式时，展开并选中完整链接供手动复制。
  }
  if (!isCurrent()) return
  copying.value = false
  copyState.value = copied ? 'success' : 'manual'
  if (copied) {
    copyTimer = setTimeout(() => {
      copyState.value = 'idle'
    }, 2000)
  } else {
    await nextTick()
    if (isCurrent()) {
      manualCopy.value?.focus()
      manualCopy.value?.select()
    }
  }
}
watch([() => props.stage.id, () => props.stage.authorizationURL], resetCopy, { flush: 'sync' })
onScopeDispose(() => {
  disposed = true
  resetCopy()
})
const device = computed(() => props.stage.method === 'device_oauth')
const placeholder = computed(() =>
  props.stage.redirectURI
    ? props.stage.redirectURI +
      (props.stage.redirectURI.includes('?') ? '&' : '?') +
      'code=…&state=…'
    : t('subscriptions.callbackPlaceholder'),
)
async function submitPastedCallback(): Promise<void> {
  await nextTick()
  if (!props.disabled && props.modelValue.trim()) emit('submit')
}
</script>

<template>
  <div class="modern-subscription-authorization">
    <section class="modern-subscription-auth-step">
      <div class="modern-subscription-auth-heading">
        <h4><span aria-hidden="true">1</span>{{ t('subscriptions.linkStep') }}</h4>
        <div v-if="stage.authorizationURL" class="modern-subscription-link-actions">
          <AppButton
            variant="outline"
            size="sm"
            :loading="copying"
            :disabled="disabled"
            @click="copyAuthorizationURL"
          >
            <span class="modern-subscription-copy-label">
              <span class="modern-subscription-copy-measure" aria-hidden="true">{{
                t('subscriptions.copyLink')
              }}</span>
              <span>{{
                t(copyState === 'success' ? 'ui.copy.success' : 'subscriptions.copyLink')
              }}</span>
            </span>
          </AppButton>
          <AppButton variant="ghost" size="sm" as-child>
            <AppExternalLink :href="stage.authorizationURL">
              <AppIcon :icon="ExternalLink" size="sm" />{{ t('subscriptions.openAuthorization') }}
            </AppExternalLink>
          </AppButton>
          <span class="modern-sr-only" role="status">{{
            copyState === 'success' ? t('ui.copy.success') : ''
          }}</span>
        </div>
      </div>
      <AppTextArea
        v-if="copyState === 'manual' && stage.authorizationURL"
        ref="manualCopy"
        :model-value="stage.authorizationURL"
        :label="t('ui.copy.manualTitle')"
        :description="t('ui.copy.manualHelp')"
        :rows="3"
        mono
        readonly
        spellcheck="false"
      />
      <div v-if="stage.authorizationURL" class="modern-subscription-auth-url">
        <AppCopyValue
          :value="stage.authorizationURL"
          :label="t('subscriptions.copyAuthorization')"
          wrap
        />
      </div>
    </section>
    <section class="modern-subscription-auth-step">
      <h4>
        <span aria-hidden="true">2</span
        >{{ t(device ? 'subscriptions.deviceStep' : 'subscriptions.callbackStep') }}
      </h4>
      <template v-if="device">
        <div v-if="stage.userCode" class="modern-subscription-device-code">
          <AppCopyValue :value="stage.userCode" :label="t('subscriptions.copyDeviceCode')" wrap />
        </div>
        <p class="modern-subscription-auth-help">{{ t('subscriptions.deviceHelp') }}</p>
      </template>
      <template v-else>
        <AppTextArea
          :model-value="modelValue"
          :label="t('subscriptions.callbackURL')"
          label-hidden
          :description="t('subscriptions.callbackHelp')"
          :placeholder="placeholder"
          :rows="3"
          mono
          :disabled="disabled"
          :error="error"
          autocomplete="off"
          spellcheck="false"
          autocapitalize="none"
          @paste="submitPastedCallback"
          @update:model-value="emit('update:modelValue', $event)"
          @keydown.ctrl.enter.prevent="emit('submit')"
          @keydown.meta.enter.prevent="emit('submit')"
        />
      </template>
    </section>
    <div class="modern-subscription-auth-footer">
      <AppButton variant="text" size="sm" :disabled="disabled" @click="emit('restart')">
        {{ t('subscriptions.restart') }}
      </AppButton>
      <AppButton
        v-if="!device"
        :icon="Send"
        variant="primary"
        :loading="submitting"
        :disabled="disabled || !modelValue.trim()"
        @click="emit('submit')"
        >{{ t('subscriptions.submitCallback') }}</AppButton
      >
    </div>
  </div>
</template>

<style scoped>
.modern-subscription-authorization,
.modern-subscription-auth-step {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-3);
}
.modern-subscription-authorization {
  gap: var(--modern-space-5);
}
.modern-subscription-auth-step h4 {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-semibold);
}
.modern-subscription-auth-step h4 > span {
  display: inline-grid;
  width: var(--modern-space-5);
  height: var(--modern-space-5);
  flex: none;
  place-items: center;
  border-radius: var(--modern-radius-round);
  background: var(--modern-accent-soft);
  color: var(--modern-badge-brand-text);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-auth-url,
.modern-subscription-device-code {
  min-width: 0;
  padding: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-secondary);
  color: var(--modern-muted);
}
.modern-subscription-device-code {
  font-size: var(--modern-font-size-section);
  color: var(--modern-text);
}
.modern-subscription-auth-help {
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
  line-height: var(--modern-leading-body);
}
.modern-subscription-auth-heading,
.modern-subscription-auth-footer,
.modern-subscription-link-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: center;
  gap: var(--modern-space-2);
}
.modern-subscription-link-actions {
  margin-inline-start: auto;
}
.modern-subscription-copy-label {
  display: grid;
}
.modern-subscription-copy-label > span {
  grid-area: 1 / 1;
}
.modern-subscription-copy-measure {
  visibility: hidden;
}
.modern-subscription-auth-heading,
.modern-subscription-auth-footer {
  justify-content: space-between;
}
.modern-subscription-auth-footer {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-3);
}
</style>
