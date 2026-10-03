<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  credentialPolicyKey,
  getCredentialPolicy,
  getGroupPolicy,
  groupPolicyKey,
  saveCredentialPolicy,
  saveGroupPolicy,
  type CredentialPolicy,
  type GroupPolicy,
} from '@modern/api/group-detail'
import type { GroupRow } from '@modern/api/groups'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppFormSection,
  AppNotice,
  AppTextArea,
} from '@modern/components/ui'
import { copyText } from '@modern/components/ui/clipboard'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import GroupWorkspacePanel from './GroupWorkspacePanel.vue'
import { parseRawJson, type JsonNode } from './policy-editor/policy-model'
import PolicyEditor from './policy-editor/PolicyEditor.vue'

const props = defineProps<{
  group: GroupRow
  credential?: { id: number; label?: string; name?: string }
}>()

const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const controller = new AbortController()

onScopeDispose(() => {
  controller.abort()
})

const isCredential = computed(() => !!props.credential)

const activeQueryKey = computed(() =>
  props.credential
    ? credentialPolicyKey(props.group.id, props.credential.id)
    : groupPolicyKey(props.group.id),
)

const query = useQuery<GroupPolicy | CredentialPolicy>({
  queryKey: activeQueryKey,
  queryFn: ({ signal }) =>
    props.credential
      ? getCredentialPolicy(client, props.group.id, props.credential.id, signal)
      : getGroupPolicy(client, props.group.id, signal),
})

const inheritedGroupQuery = useQuery({
  queryKey: computed(() => groupPolicyKey(props.group.id)),
  queryFn: ({ signal }) => getGroupPolicy(client, props.group.id, signal),
  enabled: isCredential,
})

const currentPolicy = ref<GroupPolicy | CredentialPolicy>()
const baseline = ref('')
const draft = ref('')
const saving = ref(false)
const serverError = ref('')

const dirty = computed(() => draft.value !== baseline.value)

// 保留原始词法 token 缩进美化，完全避免 JSON.parse/stringify 浮点精度损耗
function printPrettyRawJson(node: JsonNode, indent = 0): string {
  if (node.type === 'literal') return node.raw
  const pad = '  '.repeat(indent)
  const innerPad = '  '.repeat(indent + 1)
  if (node.type === 'array') {
    if (!node.items.length) return '[]'
    const items = node.items
      .map((item) => `${innerPad}${printPrettyRawJson(item, indent + 1)}`)
      .join(',\n')
    return `[\n${items}\n${pad}]`
  }
  if (!node.entries.length) return '{}'
  const entries = node.entries
    .map((e) => `${innerPad}${JSON.stringify(e.key)}: ${printPrettyRawJson(e.value, indent + 1)}`)
    .join(',\n')
  return `{\n${entries}\n${pad}}`
}

function tryFormatPrettyJson(text: string): string | null {
  const trimmed = text.trim()
  if (!trimmed) return null
  try {
    return printPrettyRawJson(parseRawJson(trimmed))
  } catch {
    return null
  }
}

watch(
  query.data,
  (data) => {
    if (!data || dirty.value || saving.value) return
    currentPolicy.value = data
    const formatted = tryFormatPrettyJson(data.configText) ?? data.configText
    baseline.value = formatted
    draft.value = formatted
    serverError.value = ''
  },
  { immediate: true },
)

const isUnconfigured = computed(
  () => !currentPolicy.value || currentPolicy.value.revisionText === '0',
)

function handleJsonBlur(): void {
  const formatted = tryFormatPrettyJson(draft.value)
  if (formatted !== null) {
    draft.value = formatted
  }
}

function handleJsonPaste(e: ClipboardEvent): void {
  const pasted = e.clipboardData?.getData('text')
  if (!pasted) return
  const target = e.target as HTMLTextAreaElement | null
  if (!target) return
  e.preventDefault()
  const start = target.selectionStart ?? 0
  const end = target.selectionEnd ?? 0
  const current = draft.value
  const combined = current.slice(0, start) + pasted + current.slice(end)
  const formatted = tryFormatPrettyJson(combined)
  draft.value = formatted !== null ? formatted : combined
}

const jsonValidation = computed<{ valid: boolean; value?: unknown; error?: string }>(() => {
  const trimmed = draft.value.trim()
  if (!trimmed) {
    return { valid: false, error: t('groupDetail.policy.invalidJson') }
  }
  try {
    const parsed = JSON.parse(trimmed)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return { valid: false, error: t('groupDetail.policy.invalidJson') }
    }
    return { valid: true, value: parsed }
  } catch {
    return { valid: false, error: t('groupDetail.policy.invalidJson') }
  }
})

const saveDisabled = computed(
  () => !dirty.value || !jsonValidation.value.valid || saving.value || !currentPolicy.value,
)

async function save(): Promise<void> {
  if (!currentPolicy.value || !dirty.value || saving.value) return
  const formatted = tryFormatPrettyJson(draft.value)
  if (formatted !== null) {
    draft.value = formatted
  }
  if (!jsonValidation.value.valid) {
    serverError.value = jsonValidation.value.error || t('groupDetail.policy.invalidJson')
    return
  }
  saving.value = true
  serverError.value = ''
  const expectedRevisionText = currentPolicy.value.revisionText

  try {
    await cache.cancelQueries({ queryKey: activeQueryKey.value })
    const result = props.credential
      ? await saveCredentialPolicy(
          client,
          props.group.id,
          props.credential.id,
          expectedRevisionText,
          draft.value,
          controller.signal,
        )
      : await saveGroupPolicy(
          client,
          props.group.id,
          expectedRevisionText,
          draft.value,
          controller.signal,
        )
    if (controller.signal.aborted) return
    currentPolicy.value = result
    const formatted = tryFormatPrettyJson(result.configText) ?? result.configText
    baseline.value = formatted
    draft.value = formatted
    cache.setQueryData(activeQueryKey.value, result)
    emit('saved')
    emit('close')
  } catch (err: unknown) {
    if (controller.signal.aborted) return
    if (
      err instanceof ApiError &&
      (err.status === 409 ||
        err.code === 'POLICY_REVISION_CONFLICT' ||
        err.code === 'POLICY_REVISION_OVERFLOW')
    ) {
      serverError.value = t('groupDetail.policy.conflict')
    } else if (err instanceof ApiError) {
      serverError.value = err.data
        ? String(err.data)
        : err.message || t('groupDetail.policy.saveFailed')
    } else {
      serverError.value = t('groupDetail.policy.saveFailed')
    }
  } finally {
    saving.value = false
  }
}

// 可视化编辑与 JSON 模式切换
const editorMode = ref<'visual' | 'json'>('visual')

// 导出与复制
const exportNotice = ref('')

async function handleExportCopy(): Promise<void> {
  try {
    const text = tryFormatPrettyJson(draft.value) ?? draft.value
    const success = await copyText(text)
    if (success) {
      exportNotice.value = t('groupDetail.policy.exported')
      setTimeout(() => {
        exportNotice.value = ''
      }, 3000)
    } else {
      serverError.value = t('groupDetail.policy.clipboardUnavailable')
    }
  } catch (err: unknown) {
    serverError.value = err instanceof Error ? err.message : String(err)
  }
}

function handleExportDownload(): void {
  try {
    const text = tryFormatPrettyJson(draft.value) ?? draft.value
    const blob = new Blob([text], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = props.credential
      ? `credential-${props.credential.id}-policy.json`
      : `group-${props.group.id}-policy.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch (err: unknown) {
    serverError.value = err instanceof Error ? err.message : String(err)
  }
}

const inheritedRules = computed(() => {
  if (!props.credential || !inheritedGroupQuery.data.value?.configText) return []
  try {
    const parsed = JSON.parse(inheritedGroupQuery.data.value.configText)
    return Array.isArray(parsed.rules)
      ? (parsed.rules as Array<{
          id?: string
          name?: string
          domain?: string
          enabled?: boolean
          then?: { type?: string; factor?: string }
        }>)
      : []
  } catch {
    return []
  }
})
</script>

<template>
  <GroupWorkspacePanel
    :title="
      props.credential
        ? props.credential.label || props.credential.name || t('groupDetail.credentialCards.policy')
        : t('groupDetail.policy.title')
    "
    :description="
      props.credential
        ? t('groupDetail.policy.credentialDescription')
        : t('groupDetail.policy.description')
    "
    :dirty="dirty"
    :pending="saving"
    :loading="query.isPending.value"
    :save-disabled="saveDisabled"
    :save-label="t('groupDetail.policy.saveLabel')"
    wide
    @close="emit('close')"
    @save="save"
  >
    <AppCollectionState v-if="query.isPending.value" :title="t('collection.loading')" loading />
    <AppCollectionState
      v-else-if="query.isError.value && !currentPolicy"
      :title="t('groupDetail.policy.loadFailed')"
      error
    >
      <AppButton @click="query.refetch()">{{ t('ui.retry') }}</AppButton>
    </AppCollectionState>
    <template v-else>
      <AppNotice v-if="serverError" tone="danger">
        {{ serverError }}
      </AppNotice>
      <AppNotice v-else-if="isUnconfigured" tone="info">
        {{ t('groupDetail.policy.emptyState') }}
      </AppNotice>

      <!-- 分组继承规则（凭据策略只读展示） -->
      <AppFormSection
        v-if="props.credential"
        compact
        :title="t('groupDetail.policy.inheritedGroup')"
      >
        <div class="modern-inherited-header">
          <span>{{
            t('groupDetail.policy.inheritedRulesCount', { count: inheritedRules.length })
          }}</span>
          <span v-if="inheritedGroupQuery.data.value" class="modern-inherited-revision">
            {{
              t('groupDetail.policy.inheritedRevision', {
                rev: inheritedGroupQuery.data.value.revisionText,
              })
            }}
          </span>
        </div>
        <div v-if="inheritedRules.length" class="modern-inherited-rules-list">
          <div v-for="rule in inheritedRules" :key="rule.id" class="modern-inherited-rule-card">
            <div class="modern-inherited-rule-header">
              <span class="modern-inherited-rule-name">{{ rule.name || rule.id }}</span>
              <AppBadge :tone="rule.enabled ? 'success' : 'neutral'" compact>
                {{ rule.enabled ? t('groupDetail.on') : t('groupDetail.off') }}
              </AppBadge>
            </div>
            <div class="modern-inherited-rule-action">
              <span>{{ rule.domain }}: {{ rule.then?.type }}</span>
              <span v-if="rule.then?.factor"> ×{{ rule.then?.factor }}</span>
            </div>
          </div>
        </div>
      </AppFormSection>

      <!-- 策略编辑工具栏与切换 -->
      <div class="modern-policy-editor">
        <div class="modern-policy-editor-toolbar">
          <div class="modern-policy-mode-tabs" role="tablist">
            <AppButton
              :variant="editorMode === 'visual' ? 'primary' : 'ghost'"
              size="sm"
              @click="editorMode = 'visual'"
            >
              {{ t('groupDetail.policy.modeVisual') }}
            </AppButton>
            <AppButton
              :variant="editorMode === 'json' ? 'primary' : 'ghost'"
              size="sm"
              @click="editorMode = 'json'"
            >
              {{ t('groupDetail.policy.modeJson') }}
            </AppButton>
          </div>
          <div class="modern-policy-portability-actions">
            <AppButton variant="outline" size="sm" @click="handleExportCopy">
              {{ t('groupDetail.policy.copyJson') }}
            </AppButton>
            <AppButton variant="outline" size="sm" @click="handleExportDownload">
              {{ t('groupDetail.policy.downloadJson') }}
            </AppButton>
          </div>
        </div>

        <AppNotice v-if="exportNotice" tone="success" compact class="mt-2">
          {{ exportNotice }}
        </AppNotice>

        <!-- 主编辑器：可视化与 JSON 切换，绑定相同 raw draft -->
        <div class="mt-3">
          <PolicyEditor
            v-if="editorMode === 'visual'"
            v-model="draft"
            :default-domain="props.credential ? 'pricing' : 'scheduling'"
            :disabled="saving"
          />
          <div v-else class="modern-policy-json-wrapper">
            <AppTextArea
              v-model="draft"
              :label="t('groupDetail.policy.rawJson')"
              :error="dirty && !jsonValidation.valid ? jsonValidation.error : undefined"
              :disabled="saving"
              mono
              :rows="14"
              @blur="handleJsonBlur"
              @paste="handleJsonPaste"
            />
            <div v-if="!draft.trim()" class="modern-policy-ghost-overlay" aria-hidden="true">
              <pre class="modern-policy-ghost-pre">{{ t('groupDetail.policy.placeholder') }}</pre>
            </div>
          </div>
        </div>
      </div>
    </template>
  </GroupWorkspacePanel>
</template>

<style scoped>
.modern-inherited-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
  margin-bottom: var(--modern-space-2);
}

.modern-inherited-revision {
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-caption);
}

.modern-inherited-rules-list {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-2);
}

.modern-inherited-rule-card {
  padding: var(--modern-space-2) var(--modern-space-3);
  background: var(--modern-subtle);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-1);
}

.modern-inherited-rule-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.modern-inherited-rule-name {
  font-weight: var(--modern-weight-medium);
  font-size: var(--modern-font-size-body);
}

.modern-inherited-rule-action {
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
  font-family: var(--modern-font-mono);
}

.modern-policy-editor {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-3);
}

.modern-policy-editor-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}

.modern-policy-json-wrapper {
  position: relative;
}

.modern-policy-ghost-overlay {
  position: absolute;
  top: calc(
    var(--modern-font-size-secondary) * var(--modern-leading-compact) + var(--modern-space-1-5) +
      var(--modern-line-width) + var(--modern-space-2)
  );
  left: calc(var(--modern-line-width) + var(--modern-space-3));
  pointer-events: none;
  user-select: none;
}

.modern-policy-ghost-pre {
  margin: 0;
  padding: 0;
  font-family: var(--modern-font-mono);
  font-size: var(--modern-font-size-body);
  line-height: var(--modern-leading-body);
  color: var(--modern-muted);
  white-space: pre;
}

.modern-policy-mode-tabs {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1);
  background: var(--modern-subtle);
  padding: var(--modern-space-0-5);
  border-radius: var(--modern-radius-control);
  border: var(--modern-line-width) solid var(--modern-border);
}

.modern-policy-portability-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}
</style>
