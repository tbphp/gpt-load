<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  credentialPolicyKey,
  getPolicy,
  getPolicyDiscovery,
  groupPolicyKey,
  policyDiscoveryKey,
  savePolicy,
  type PolicyConfig,
} from '@modern/api/group-detail'
import type { GroupRow } from '@modern/api/groups'
import {
  AppButton,
  AppCollectionState,
  AppFormSection,
  AppNotice,
  AppSegmentedField,
  AppTextArea,
} from '@modern/components/ui'
import { copyText } from '@modern/components/ui/clipboard'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import GroupWorkspacePanel from './GroupWorkspacePanel.vue'
import {
  getField,
  groupPolicyMode,
  parseRawJson,
  setGroupPolicyMode,
  type GroupPolicyMode,
  type JsonNode,
  type JsonObjectNode,
} from './policy-editor/policy-model'
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

const query = useQuery<PolicyConfig>({
  queryKey: activeQueryKey,
  queryFn: ({ signal }) => getPolicy(client, props.group.id, signal, props.credential?.id),
})

const inheritedGroupQuery = useQuery({
  queryKey: computed(() => groupPolicyKey(props.group.id)),
  queryFn: ({ signal }) => getPolicy(client, props.group.id, signal),
  enabled: isCredential,
})

const currentPolicy = ref<PolicyConfig>()
const baseline = ref('')
const draft = ref('')
const saving = ref(false)
const serverError = ref('')

const serializedDirty = computed(() => draft.value !== baseline.value)
const hasLocalDraft = computed(
  () =>
    !visualDraftStatus.value.valid ||
    visualDraftStatus.value.pending ||
    visualDraftStatus.value.hasInvalid,
)
const dirty = computed(() => serializedDirty.value || hasLocalDraft.value)

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

const parsedDraftAst = computed<JsonObjectNode | null>(() => {
  const trimmed = draft.value.trim()
  if (!trimmed) return null
  try {
    const node = parseRawJson(trimmed)
    return node.type === 'object' ? node : null
  } catch {
    return null
  }
})

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

  // 凭据覆盖模式下粘贴：若粘贴的 JSON 对象未显式包含 group_policy，保留当前 override 意图
  if (props.credential && credentialPolicyMode.value === 'override') {
    try {
      const ast = parseRawJson(combined.trim())
      if (ast.type === 'object' && !getField(ast, 'group_policy')) {
        draft.value = printPrettyRawJson(setGroupPolicyMode(ast, 'override'))
        return
      }
    } catch {
      // 语法错误由下方兜底处理
    }
  }

  const formatted = tryFormatPrettyJson(combined)
  draft.value = formatted !== null ? formatted : combined
}

const jsonValidation = computed<{ valid: boolean; error?: string }>(() => {
  if (!draft.value.trim() || !parsedDraftAst.value) {
    return { valid: false, error: t('groupDetail.policy.invalidJson') }
  }
  return { valid: true }
})

const visualDraftStatus = ref<{ valid: boolean; pending: boolean; hasInvalid: boolean }>({
  valid: true,
  pending: false,
  hasInvalid: false,
})

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

function handleVisualDraftStatus(status: {
  valid: boolean
  pending: boolean
  hasInvalid: boolean
}): void {
  visualDraftStatus.value = status
}

function switchEditorMode(mode: 'visual' | 'json'): void {
  if (saving.value) return
  if (mode === 'json') {
    if (
      !visualDraftStatus.value.valid ||
      visualDraftStatus.value.hasInvalid ||
      visualDraftStatus.value.pending
    ) {
      serverError.value = t('groupDetail.policy.invalidDraftBlockJson')
      return
    }
    handleJsonBlur()
  }
  serverError.value = ''
  editorMode.value = mode
}

const saveDisabled = computed(
  () =>
    !serializedDirty.value ||
    !jsonValidation.value.valid ||
    saving.value ||
    !currentPolicy.value ||
    !visualDraftStatus.value.valid ||
    visualDraftStatus.value.pending ||
    visualDraftStatus.value.hasInvalid,
)

async function save(): Promise<void> {
  if (!currentPolicy.value || !serializedDirty.value || saving.value) return
  if (!visualDraftStatus.value.valid || visualDraftStatus.value.hasInvalid) {
    serverError.value = t('groupDetail.policy.invalidDraftBlockJson')
    return
  }
  if (visualDraftStatus.value.pending) return
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
  const targetGroupID = props.group.id
  const targetCredentialID = props.credential?.id
  const queryKey = activeQueryKey.value
  const textToSave = tryFormatPrettyJson(draft.value) ?? draft.value

  try {
    await cache.cancelQueries({ queryKey })
    if (controller.signal.aborted) return
    const result = await savePolicy(
      client,
      targetGroupID,
      expectedRevisionText,
      textToSave,
      controller.signal,
      targetCredentialID,
    )
    if (controller.signal.aborted) return
    currentPolicy.value = result
    const formatted = tryFormatPrettyJson(result.configText) ?? result.configText
    baseline.value = formatted
    draft.value = formatted
    cache.setQueryData(queryKey, result)
    emit('saved')
    emit('close')
  } catch (err: unknown) {
    if (controller.signal.aborted) return
    saving.value = false
    if (
      err instanceof ApiError &&
      (err.status === 409 ||
        err.code === 'POLICY_REVISION_CONFLICT' ||
        err.code === 'POLICY_REVISION_OVERFLOW')
    ) {
      serverError.value = t('groupDetail.policy.conflict')
      void cache.invalidateQueries({ queryKey })
    } else if (err instanceof ApiError) {
      serverError.value = err.data
        ? String(err.data)
        : err.message || t('groupDetail.policy.saveFailed')
    } else {
      serverError.value = t('groupDetail.policy.saveFailed')
    }
  } finally {
    if (!controller.signal.aborted) saving.value = false
  }
}

// 可视化编辑与 JSON 模式切换
const editorMode = ref<'visual' | 'json'>('visual')

// 隐藏配额时间窗口选项发现（全组观测，按需传递给规则编辑器，不渲染 reference UI）
const discoveryQueryKey = computed(() => policyDiscoveryKey(props.group.id, props.credential?.id))
const discoveryQuery = useQuery({
  queryKey: discoveryQueryKey,
  queryFn: ({ signal }) => getPolicyDiscovery(client, signal, props.group.id, props.credential?.id),
})
const quotaWindows = computed<readonly number[]>(
  () => discoveryQuery.data.value?.quota_windows ?? [],
)

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

// 凭据继承/覆盖控制：以顶层 group_policy 字段（"inherit" | "override"）为准
const credentialPolicyModeOptions = computed(() => [
  { value: 'inherit', label: t('groupDetail.inherit') },
  { value: 'override', label: t('groupDetail.override') },
])

const isRootObjectValid = computed(() => !draft.value.trim() || parsedDraftAst.value !== null)

const credentialPolicyMode = computed<GroupPolicyMode>({
  get() {
    return groupPolicyMode(parsedDraftAst.value ?? undefined)
  },
  set(newMode: GroupPolicyMode) {
    handleCredentialPolicyModeChange(newMode)
  },
})

function handleCredentialPolicyModeChange(newMode: GroupPolicyMode): void {
  const trimmed = draft.value.trim()
  try {
    // 显式模式切换：空草稿基于标准空规范初始化，非空则保留所有其他字段更新 group_policy
    const baseText = trimmed || '{\n  "schema_version": 1,\n  "rules": []\n}'
    const ast = parseRawJson(baseText)
    if (ast.type !== 'object') return
    draft.value = printPrettyRawJson(setGroupPolicyMode(ast, newMode))
  } catch {
    // 语法错误时保留 raw 文本不静默覆盖
  }
}

const groupPolicyConfigText = computed(() => inheritedGroupQuery.data.value?.configText ?? '')
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
      <AppNotice v-if="serverError" tone="danger" class="modern-policy-server-error">
        <div>{{ serverError }}</div>
      </AppNotice>

      <AppFormSection
        compact
        :title="
          props.credential ? t('groupDetail.policy.sectionTitle') : t('groupDetail.policy.title')
        "
      >
        <template v-if="props.credential" #actions>
          <AppSegmentedField
            v-model="credentialPolicyMode"
            class="modern-policy-mode-select"
            :label="t('groupDetail.policy.sectionTitle')"
            label-hidden
            :options="credentialPolicyModeOptions"
            size="xs"
            :disabled="saving || !isRootObjectValid"
          />
        </template>

        <!-- 凭据继承模式：展示分组只读策略，若分组策略加载失败显示 inline notice 不阻塞本地编辑 -->
        <div
          v-if="props.credential && credentialPolicyMode === 'inherit'"
          class="modern-policy-inherited-block"
        >
          <div class="modern-policy-section-header">
            <span class="modern-policy-section-title">{{ t('groupDetail.policy.fromGroup') }}</span>
          </div>
          <AppNotice v-if="inheritedGroupQuery.isError.value" tone="warning" compact>
            {{ t('groupDetail.policy.loadFailed') }}
            <AppButton size="xs" @click="inheritedGroupQuery.refetch()">
              {{ t('ui.retry') }}
            </AppButton>
          </AppNotice>
          <PolicyEditor
            v-else-if="groupPolicyConfigText"
            :model-value="groupPolicyConfigText"
            disabled
            default-domain="scheduling"
            :quota-windows="quotaWindows"
          />
        </div>

        <div
          v-if="props.credential && credentialPolicyMode === 'inherit'"
          class="modern-policy-section-header mb-2"
        >
          <span class="modern-policy-section-title">{{
            t('groupDetail.policy.accountRules')
          }}</span>
        </div>

        <!-- 策略编辑区：支持可视化与 JSON 源码编辑，凭据继承与覆盖均可追加编辑自身规则 -->
        <div class="modern-policy-editor">
          <div class="modern-policy-editor-toolbar">
            <div class="modern-policy-mode-tabs" role="tablist">
              <AppButton
                :variant="editorMode === 'visual' ? 'primary' : 'ghost'"
                size="xs"
                :disabled="saving"
                @click="switchEditorMode('visual')"
              >
                {{ t('groupDetail.policy.modeVisual') }}
              </AppButton>
              <AppButton
                :variant="editorMode === 'json' ? 'primary' : 'ghost'"
                size="xs"
                :disabled="saving"
                @click="switchEditorMode('json')"
              >
                {{ t('groupDetail.policy.modeJson') }}
              </AppButton>
            </div>
            <div class="modern-policy-portability-actions">
              <AppButton variant="outline" size="xs" @click="handleExportCopy">
                {{ t('groupDetail.policy.copyJson') }}
              </AppButton>
              <AppButton variant="outline" size="xs" @click="handleExportDownload">
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
              :quota-windows="quotaWindows"
              @draft-status="handleVisualDraftStatus"
            />
            <div v-else class="modern-policy-json-wrapper">
              <AppTextArea
                v-model="draft"
                :label="t('groupDetail.policy.rawJson')"
                :error="serializedDirty && !jsonValidation.valid ? jsonValidation.error : undefined"
                :disabled="saving"
                mono
                :rows="14"
                @blur="handleJsonBlur"
                @paste="handleJsonPaste"
              />
              <div v-if="!draft.trim()" class="modern-policy-ghost-overlay" aria-hidden="true">
                <pre class="modern-policy-ghost-pre">{{
                  '{\n  "schema_version": 1,\n  "rules": []\n}'
                }}</pre>
              </div>
            </div>
          </div>
        </div>
      </AppFormSection>
    </template>
  </GroupWorkspacePanel>
</template>

<style scoped>
.modern-policy-mode-select {
  width: var(--modern-menu-min-width);
}

.modern-policy-inherited-block {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-2);
  margin-bottom: var(--modern-space-3);
  padding-bottom: var(--modern-space-3);
  border-bottom: var(--modern-line-width) solid var(--modern-border);
}

.modern-policy-section-header {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}

.modern-policy-section-title {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
  font-weight: var(--modern-weight-medium);
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

.modern-policy-server-error {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-2);
}
</style>
