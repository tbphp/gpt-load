<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  credentialPolicyKey,
  getCredentialPolicy,
  getGroupPolicy,
  getPolicyDiscovery,
  groupPolicyKey,
  policyDiscoveryKey,
  previewCredentialPolicy,
  previewGroupPolicy,
  saveCredentialPolicy,
  saveGroupPolicy,
  type CredentialPolicy,
  type GroupPolicy,
  type PolicyDiscovery,
  type PolicyPreviewResponse,
} from '@modern/api/group-detail'
import type { GroupRow } from '@modern/api/groups'
import {
  AppBadge,
  AppButton,
  AppCollectionState,
  AppFormSection,
  AppNotice,
  AppSegmentedControl,
  AppTextArea,
  AppTextField,
} from '@modern/components/ui'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import GroupWorkspacePanel from './GroupWorkspacePanel.vue'
import PolicyEditor from './policy-editor/PolicyEditor.vue'
import {
  exportPortablePolicy,
  importPortablePolicy,
  type PolicyDomain,
  type PolicyImportMode,
} from './policy-portability'

const props = defineProps<{
  group: GroupRow
  credential?: { id: number; label?: string; name?: string }
}>()

const emit = defineEmits<{ close: []; saved: [] }>()
const { t, locale } = useI18n()
const client = useApiClient()
const cache = useQueryClient()
const controller = new AbortController()
let previewController = new AbortController()

function invalidatePreview(): void {
  previewController.abort()
  previewSequence++
  previewData.value = null
  previewError.value = ''
  previewLoading.value = false
}

onScopeDispose(() => {
  controller.abort()
  invalidatePreview()
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

watch(
  query.data,
  (data) => {
    if (!data || dirty.value || saving.value) return
    currentPolicy.value = data
    baseline.value = data.configText
    draft.value = data.configText
    serverError.value = ''
  },
  { immediate: true },
)

const isUnconfigured = computed(
  () => !currentPolicy.value || currentPolicy.value.revisionText === '0',
)

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
    baseline.value = result.configText
    draft.value = result.configText
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
const editorModeOptions = computed(() => [
  { label: t('groupDetail.policy.modeVisual'), value: 'visual' },
  { label: t('groupDetail.policy.modeJson'), value: 'json' },
])

// 导出功能
const exportNotice = ref('')

async function handleExportCopy(): Promise<void> {
  try {
    const result = exportPortablePolicy(draft.value)
    if (!navigator?.clipboard?.writeText) {
      throw new Error(t('groupDetail.policy.clipboardUnavailable'))
    }
    await navigator.clipboard.writeText(result.text)
    exportNotice.value = t('groupDetail.policy.exported')
    setTimeout(() => {
      exportNotice.value = ''
    }, 3000)
  } catch (err: unknown) {
    serverError.value = err instanceof Error ? err.message : String(err)
  }
}

function handleExportDownload(): void {
  try {
    const result = exportPortablePolicy(draft.value)
    const blob = new Blob([result.text], { type: 'application/json' })
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

// 导入功能
const showImportSection = ref(false)
const importRawText = ref('')
const importDomain = ref<PolicyDomain>(props.credential ? 'pricing' : 'scheduling')
const importDomainOptions = computed(() => [
  { label: t('groupDetail.policy.scheduling'), value: 'scheduling' },
  { label: t('groupDetail.policy.pricing'), value: 'pricing' },
])
const importMode = ref<PolicyImportMode>('append')
const importModeOptions = computed(() => [
  { label: t('groupDetail.policy.importAppend'), value: 'append' },
  { label: t('groupDetail.policy.importReplace'), value: 'replace' },
])
const importError = ref('')
const importSuccessNotice = ref('')
const importWarnings = ref<string[]>([])

function executeImport(): void {
  if (saving.value) return
  importError.value = ''
  importWarnings.value = []
  importSuccessNotice.value = ''
  const trimmed = importRawText.value.trim()
  if (!trimmed) {
    importError.value = t('groupDetail.policy.invalidJson')
    return
  }
  try {
    const result = importPortablePolicy(draft.value, trimmed, {
      domain: importDomain.value,
      mode: importMode.value,
    })
    draft.value = result.text
    const renames = result.renamed.map((r) => `${r.from} -> ${r.to}`)
    const warnings = result.warnings.map((w) => `${w.path ? w.path + ': ' : ''}${w.message}`)
    importWarnings.value = [...renames, ...warnings]
    importSuccessNotice.value = t('groupDetail.policy.importSuccess', { count: result.imported })
    showImportSection.value = false
    importRawText.value = ''
  } catch (err: unknown) {
    importError.value = err instanceof Error ? err.message : String(err)
  }
}

// 预览状态管理
const previewRequestModel = ref('')
const previewSimulatedTime = ref('')
const previewLoading = ref(false)
const previewError = ref('')
const previewData = ref<PolicyPreviewResponse | null>(null)
let previewSequence = 0

watch([draft, previewRequestModel, previewSimulatedTime], () => {
  invalidatePreview()
})

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

const actualInheritedRev = computed(() => {
  if (!props.credential || !previewData.value?.candidates?.length) return undefined
  for (const cand of previewData.value.candidates) {
    for (const target of cand.targets) {
      for (const rule of target.group_rules) {
        if (rule.revision_text) return rule.revision_text
      }
    }
  }
  return undefined
})

// 参数参考发现与本地化描述字典
const DESCRIPTOR_TRANSLATIONS: Record<string, Record<string, string>> = {
  'fact.credential.quota.remaining_ratio.label': {
    'zh-CN': '凭据配额剩余比例',
    'en-US': 'Credential Quota Remaining Ratio',
    'ja-JP': 'クレデンシャルクォータ残量割合',
  },
  'fact.credential.quota.remaining_ratio.desc': {
    'zh-CN': '凭据在指定时间窗口内的最小剩余配额比例（0.0 ~ 1.0）',
    'en-US': 'Minimum remaining quota ratio of credential in time window (0.0 to 1.0)',
    'ja-JP': '指定時間枠内のクレデンシャル最小クォータ残量割合 (0.0〜1.0)',
  },
  'fact.request.model.label': {
    'zh-CN': '请求模型',
    'en-US': 'Request Model',
    'ja-JP': 'リクエストモデル',
  },
  'fact.request.model.desc': {
    'zh-CN': '客户端请求传入的模型名称',
    'en-US': 'Model name provided in the client request',
    'ja-JP': 'クライアントリクエストで指定されたモデル名',
  },
  'fact.upstream.model.label': {
    'zh-CN': '上游模型',
    'en-US': 'Upstream Model',
    'ja-JP': 'アップストリームモデル',
  },
  'fact.upstream.model.desc': {
    'zh-CN': '实际路由匹配的分组上游模型 ID',
    'en-US': 'Upstream target model ID mapped in the group',
    'ja-JP': 'グループ内でマッピングされたアップストリームターゲットモデルID',
  },
  'predicate.time_window.label': {
    'zh-CN': '时间窗口条件',
    'en-US': 'Time Window Condition',
    'ja-JP': '時間枠条件',
  },
  'predicate.time_window.desc': {
    'zh-CN': '基于请求时刻或模拟时间的周期/星期/小时匹配',
    'en-US': 'Time-of-day / day-of-week matching anchored to current or simulated time',
    'ja-JP': '現在時刻またはシミュレーション時間に基づく曜日・時間帯の一致判定',
  },
  'action.exclude_candidate.label': {
    'zh-CN': '排除候选',
    'en-US': 'Exclude Candidate',
    'ja-JP': '候補除外',
  },
  'action.exclude_candidate.desc': {
    'zh-CN': '将匹配的凭据从当前请求的候选列表中排除',
    'en-US': 'Exclude matched credential from candidate pool for this request',
    'ja-JP': 'このリクエストの候補プールから一致したクレデンシャルを除外',
  },
  'action.multiply_price.label': {
    'zh-CN': '价格倍率调整',
    'en-US': 'Multiply Price',
    'ja-JP': '価格乗数調整',
  },
  'action.multiply_price.desc': {
    'zh-CN': '对命中请求的基础费用应用乘数因子（6位定点小数）',
    'en-US': 'Apply multiplier factor (6-decimal fixed-point) to base cost',
    'ja-JP': '基準コストに価格乗数（小数第6位固定）を適用',
  },
}

function translateDescriptor(id: string | undefined): string {
  if (!id) return ''
  const currentLoc = (locale?.value || 'en-US') as 'zh-CN' | 'en-US' | 'ja-JP'
  const entry = DESCRIPTOR_TRANSLATIONS[id]
  if (entry) {
    return entry[currentLoc] || entry['en-US'] || entry['zh-CN'] || id
  }
  return String(id).replace(/<[^>]*>/g, '')
}

const discoveryKey = policyDiscoveryKey()
const discoveryQuery = useQuery({
  queryKey: discoveryKey,
  queryFn: ({ signal }) => getPolicyDiscovery(client, signal),
})
const discovery = computed<PolicyDiscovery | undefined>(() => discoveryQuery.data.value)
const sortedParameters = computed(() => {
  if (!discovery.value?.parameters) return []
  return [...discovery.value.parameters].sort((a, b) => a.key.localeCompare(b.key))
})

async function runPreview(): Promise<void> {
  const model = previewRequestModel.value.trim()
  if (!model) {
    previewError.value = t('groupDetail.policy.requestModelRequired')
    return
  }
  if (!jsonValidation.value.valid) {
    previewError.value = jsonValidation.value.error || t('groupDetail.policy.invalidJson')
    return
  }
  const configToSend = draft.value.trim()

  previewController.abort()
  const runCtrl = new AbortController()
  previewController = runCtrl
  const seq = ++previewSequence
  const capturedDraft = draft.value
  const capturedModel = previewRequestModel.value
  const capturedSim = previewSimulatedTime.value
  const capturedGroupId = props.group.id
  const capturedCredId = props.credential?.id
  previewLoading.value = true
  previewError.value = ''

  try {
    const result = props.credential
      ? await previewCredentialPolicy(
          client,
          props.group.id,
          props.credential.id,
          model,
          previewSimulatedTime.value.trim() || null,
          configToSend,
          runCtrl.signal,
        )
      : await previewGroupPolicy(
          client,
          props.group.id,
          model,
          previewSimulatedTime.value.trim() || null,
          configToSend,
          runCtrl.signal,
        )
    if (
      runCtrl.signal.aborted ||
      seq !== previewSequence ||
      props.group.id !== capturedGroupId ||
      props.credential?.id !== capturedCredId ||
      draft.value !== capturedDraft ||
      previewRequestModel.value !== capturedModel ||
      previewSimulatedTime.value !== capturedSim
    ) {
      return
    }
    previewData.value = result
  } catch (err: unknown) {
    if (
      runCtrl.signal.aborted ||
      seq !== previewSequence ||
      props.group.id !== capturedGroupId ||
      props.credential?.id !== capturedCredId ||
      draft.value !== capturedDraft ||
      previewRequestModel.value !== capturedModel ||
      previewSimulatedTime.value !== capturedSim
    ) {
      return
    }
    if (err instanceof ApiError) {
      previewError.value = err.data
        ? String(err.data)
        : err.message || t('groupDetail.policy.previewFailed')
    } else {
      previewError.value = t('groupDetail.policy.previewFailed')
    }
  } finally {
    if (!runCtrl.signal.aborted && seq === previewSequence) {
      previewLoading.value = false
    }
  }
}
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
        <AppNotice
          v-if="
            inheritedGroupQuery.data.value &&
            actualInheritedRev &&
            inheritedGroupQuery.data.value.revisionText !== actualInheritedRev
          "
          tone="warning"
          compact
          class="mb-3"
        >
          {{
            t('groupDetail.policy.inheritedDbDesync', {
              dbRev: inheritedGroupQuery.data.value.revisionText,
              previewRev: actualInheritedRev,
            })
          }}
        </AppNotice>
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
      <AppFormSection compact :title="t('groupDetail.policy.rawJson')">
        <div class="modern-policy-editor-toolbar">
          <AppSegmentedControl
            v-model="editorMode"
            :options="editorModeOptions"
            :label="t('groupDetail.policy.editorMode')"
          />
          <div class="modern-policy-portability-actions">
            <AppButton variant="outline" size="sm" @click="handleExportCopy">
              {{ t('groupDetail.policy.copyJson') }}
            </AppButton>
            <AppButton variant="outline" size="sm" @click="handleExportDownload">
              {{ t('groupDetail.policy.downloadJson') }}
            </AppButton>
            <AppButton variant="outline" size="sm" @click="showImportSection = !showImportSection">
              {{ t('groupDetail.policy.import') }}
            </AppButton>
          </div>
        </div>

        <AppNotice v-if="exportNotice" tone="success" compact class="mt-2">
          {{ exportNotice }}
        </AppNotice>
        <AppNotice v-if="importSuccessNotice" tone="success" compact class="mt-2">
          {{ importSuccessNotice }}
        </AppNotice>
        <AppNotice v-if="importWarnings.length" tone="warning" compact class="mt-2">
          <div>{{ t('groupDetail.policy.importWarnings') }}</div>
          <ul class="modern-policy-warning-list">
            <li v-for="(w, idx) in importWarnings" :key="idx">{{ w }}</li>
          </ul>
        </AppNotice>

        <!-- 导入面板 -->
        <div v-if="showImportSection" class="modern-policy-import-card mt-3">
          <div class="modern-policy-import-header">
            <strong>{{ t('groupDetail.policy.importDialogTitle') }}</strong>
            <AppButton variant="ghost" size="sm" @click="showImportSection = false">
              {{ t('ui.close') }}
            </AppButton>
          </div>
          <AppTextArea
            v-model="importRawText"
            :label="t('groupDetail.policy.importDialogTitle')"
            label-hidden
            :placeholder="t('groupDetail.policy.importJsonPlaceholder')"
            :rows="6"
            mono
            class="mt-2"
          />
          <div class="modern-policy-import-controls mt-2">
            <AppSegmentedControl
              v-model="importDomain"
              :options="importDomainOptions"
              :label="t('groupDetail.policy.importDomain')"
            />
            <AppSegmentedControl
              v-model="importMode"
              :options="importModeOptions"
              :label="t('groupDetail.policy.importMode')"
            />
          </div>
          <AppNotice v-if="importError" tone="danger" compact class="mt-2">
            {{ importError }}
          </AppNotice>
          <div class="modern-policy-import-actions mt-3">
            <AppButton
              variant="outline"
              size="sm"
              :disabled="saving || !importRawText.trim()"
              @click="executeImport"
            >
              {{ t('groupDetail.policy.importExecute') }}
            </AppButton>
          </div>
        </div>

        <!-- 主编辑器：可视化与 JSON 切换，绑定相同 raw draft -->
        <div class="mt-3">
          <PolicyEditor
            v-if="editorMode === 'visual'"
            v-model="draft"
            :default-domain="props.credential ? 'pricing' : 'scheduling'"
            :disabled="saving"
          />
          <AppTextArea
            v-else
            v-model="draft"
            :label="t('groupDetail.policy.rawJson')"
            :hint="t('groupDetail.policy.rawJsonHint')"
            :error="dirty && !jsonValidation.valid ? jsonValidation.error : undefined"
            :placeholder="t('groupDetail.policy.placeholder')"
            :disabled="saving"
            mono
            :rows="14"
          />
        </div>
      </AppFormSection>

      <AppFormSection v-if="discovery" compact :title="t('groupDetail.policy.referenceToggle')">
        <details class="modern-policy-reference-details">
          <summary class="modern-policy-reference-summary">
            {{ t('groupDetail.policy.referenceToggle') }}
          </summary>
          <div class="modern-policy-reference-content">
            <div v-if="discovery.capabilities" class="modern-policy-reference-section">
              <div class="modern-policy-reference-heading">
                {{ t('groupDetail.policy.capabilities') }}
              </div>
              <div class="modern-policy-param-tags">
                <span class="modern-policy-param-tag">
                  {{ t('groupDetail.policy.capAccountWise') }}:
                  {{ discovery.capabilities.account_wise ? 'yes' : 'no' }}
                </span>
                <span class="modern-policy-param-tag">
                  {{ t('groupDetail.policy.capGroupAggregation') }}:
                  {{ discovery.capabilities.group_aggregation ? 'yes' : 'no' }}
                </span>
                <span class="modern-policy-param-tag">
                  {{ t('groupDetail.policy.capRecovery') }}:
                  {{ discovery.capabilities.fixed_recovery }}
                </span>
                <span class="modern-policy-param-tag">
                  {{ t('groupDetail.policy.capLivePricing') }}:
                  {{ discovery.capabilities.live_dynamic_pricing ? 'yes' : 'false' }}
                </span>
              </div>
            </div>
            <div class="modern-policy-reference-section">
              <div class="modern-policy-reference-heading">
                {{ t('groupDetail.policy.parameters') }}
              </div>
              <div class="modern-policy-param-table">
                <div
                  v-for="param in sortedParameters"
                  :key="param.key"
                  class="modern-policy-param-row"
                >
                  <div class="modern-policy-param-key">{{ param.key }}</div>
                  <div class="modern-policy-param-meta">
                    <div>
                      <strong>{{ translateDescriptor(param.label) || param.key }}</strong>
                    </div>
                    <div class="modern-policy-param-desc">
                      {{ translateDescriptor(param.description) }}
                    </div>
                    <div class="modern-policy-param-tags">
                      <span class="modern-policy-param-tag">Type: {{ param.type }}</span>
                      <span v-if="param.unit" class="modern-policy-param-tag">
                        Unit: {{ param.unit }}
                      </span>
                      <span class="modern-policy-param-tag">
                        Operators: {{ param.operators.join(', ') }}
                      </span>
                      <span class="modern-policy-param-tag">
                        Domains: {{ param.domains.join(', ') }}
                      </span>
                      <span class="modern-policy-param-tag">
                        Scopes: {{ param.binding_scopes.join(', ') }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </details>
      </AppFormSection>

      <AppFormSection compact :title="t('groupDetail.policy.preview')">
        <div class="modern-policy-preview-inputs">
          <AppTextField
            v-model="previewRequestModel"
            :label="t('groupDetail.policy.previewModel')"
            :placeholder="t('groupDetail.policy.previewModelPlaceholder')"
          />
          <AppTextField
            v-model="previewSimulatedTime"
            :label="t('groupDetail.policy.simulatedTime')"
            :placeholder="t('groupDetail.policy.simulatedTimePlaceholder')"
            :hint="t('groupDetail.policy.simulatedTimeHint')"
          />
          <div class="modern-policy-preview-action">
            <AppButton
              variant="outline"
              :loading="previewLoading"
              :disabled="previewLoading || !jsonValidation.valid || !previewRequestModel.trim()"
              @click="runPreview"
            >
              {{ t('groupDetail.policy.runPreview') }}
            </AppButton>
          </div>
        </div>

        <AppNotice v-if="previewError" tone="danger" compact class="mt-3">
          {{ previewError }}
        </AppNotice>

        <div v-if="previewData" class="modern-policy-preview-results mt-4">
          <div v-if="previewData.caveat_codes?.length" class="modern-policy-caveats mb-3">
            <AppNotice
              v-for="caveat in previewData.caveat_codes"
              :key="caveat"
              tone="warning"
              compact
              class="mb-2"
            >
              {{ t(`groupDetail.policy.caveat_${caveat.replace(/[^a-zA-Z0-9]/g, '_')}`, caveat) }}
            </AppNotice>
          </div>

          <div
            v-for="candidate in previewData.candidates"
            :key="candidate.credential_id"
            class="modern-policy-candidate-card"
          >
            <div class="modern-policy-candidate-header">
              <span class="modern-policy-candidate-name">
                {{ candidate.credential_name }} (#{{ candidate.credential_id }})
              </span>
              <span v-if="candidate.credential_version" class="modern-policy-candidate-version">
                v{{ candidate.credential_version }}
              </span>
            </div>

            <div
              v-for="target in candidate.targets"
              :key="target.upstream_model"
              class="modern-policy-target-card"
            >
              <div class="modern-policy-target-header">
                <span class="modern-policy-target-model">{{ target.upstream_model }}</span>
                <div class="modern-policy-target-badges">
                  <AppBadge :tone="target.available ? 'success' : 'neutral'" compact>
                    {{
                      target.available
                        ? t('groupDetail.policy.available')
                        : t('groupDetail.policy.unavailable')
                    }}
                  </AppBadge>
                  <AppBadge v-if="target.scheduling.excluded" tone="danger" compact>
                    {{ t('groupDetail.policy.excluded') }}
                  </AppBadge>
                  <AppBadge v-if="target.pricing.cumulative_multiplier" tone="brand" compact>
                    ×{{ target.pricing.cumulative_multiplier }}
                  </AppBadge>
                </div>
              </div>

              <!-- 分组生效规则 -->
              <div v-if="target.group_rules.length" class="modern-policy-rules-block">
                <div class="modern-policy-rules-heading">
                  {{ t('groupDetail.policy.groupRules') }}
                </div>
                <div
                  v-for="r in target.group_rules"
                  :key="r.rule_id"
                  class="modern-policy-rule-item"
                >
                  <div class="modern-policy-rule-header">
                    <span class="modern-policy-rule-name">
                      {{ r.name_snapshot || r.rule_id }}
                    </span>
                    <div class="modern-policy-rule-badges">
                      <AppBadge :tone="r.provenance === 'draft' ? 'warning' : 'neutral'" compact>
                        {{
                          r.provenance === 'draft'
                            ? t('groupDetail.policy.draft')
                            : `rev ${r.revision_text}`
                        }}
                      </AppBadge>
                      <AppBadge
                        :tone="
                          r.status === 'hit'
                            ? 'success'
                            : r.status === 'miss'
                              ? 'neutral'
                              : 'warning'
                        "
                        compact
                      >
                        {{ r.status }}
                      </AppBadge>
                    </div>
                  </div>
                  <div class="modern-policy-rule-detail">
                    <span class="modern-policy-rule-kind">{{ r.condition.kind }}</span>
                    <span v-if="r.condition.fact" class="modern-policy-rule-fact">
                      ({{ r.condition.fact }})
                    </span>
                    <span class="modern-policy-rule-action">
                      &rarr; {{ r.action.type }}
                      <template v-if="r.action.multiplier"> ×{{ r.action.multiplier }} </template>
                      <template v-else-if="r.action.factor"> ×{{ r.action.factor }} </template>
                    </span>
                  </div>
                </div>
              </div>

              <!-- 凭据生效规则 -->
              <div v-if="target.credential_rules.length" class="modern-policy-rules-block">
                <div class="modern-policy-rules-heading">
                  {{ t('groupDetail.policy.credentialRules') }}
                </div>
                <div
                  v-for="r in target.credential_rules"
                  :key="r.rule_id"
                  class="modern-policy-rule-item"
                >
                  <div class="modern-policy-rule-header">
                    <span class="modern-policy-rule-name">
                      {{ r.name_snapshot || r.rule_id }}
                    </span>
                    <div class="modern-policy-rule-badges">
                      <AppBadge :tone="r.provenance === 'draft' ? 'warning' : 'neutral'" compact>
                        {{
                          r.provenance === 'draft'
                            ? t('groupDetail.policy.draft')
                            : `rev ${r.revision_text}`
                        }}
                      </AppBadge>
                      <AppBadge
                        :tone="
                          r.status === 'hit'
                            ? 'success'
                            : r.status === 'miss'
                              ? 'neutral'
                              : 'warning'
                        "
                        compact
                      >
                        {{ r.status }}
                      </AppBadge>
                    </div>
                  </div>
                  <div class="modern-policy-rule-detail">
                    <span class="modern-policy-rule-kind">{{ r.condition.kind }}</span>
                    <span v-if="r.condition.fact" class="modern-policy-rule-fact">
                      ({{ r.condition.fact }})
                    </span>
                    <span class="modern-policy-rule-action">
                      &rarr; {{ r.action.type }}
                      <template v-if="r.action.multiplier"> ×{{ r.action.multiplier }} </template>
                      <template v-else-if="r.action.factor"> ×{{ r.action.factor }} </template>
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </AppFormSection>
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

.modern-policy-editor-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}

.modern-policy-portability-actions {
  display: flex;
  align-items: center;
  gap: var(--modern-space-2);
}

.modern-policy-import-card {
  padding: var(--modern-space-3);
  background: var(--modern-subtle);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
}

.modern-policy-import-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.modern-policy-import-controls {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-3);
}

.modern-policy-import-actions {
  display: flex;
  justify-content: flex-end;
}

.modern-policy-warning-list {
  margin: var(--modern-space-1) 0 0 var(--modern-space-3);
  padding: 0;
  font-size: var(--modern-font-size-caption);
}

.modern-policy-preview-inputs {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-3);
}

.modern-policy-preview-action {
  display: flex;
  justify-content: flex-end;
}

.modern-policy-candidate-card {
  padding: var(--modern-space-3);
  background: var(--modern-subtle);
  border-radius: var(--modern-radius-panel);
  margin-bottom: var(--modern-space-3);
}

.modern-policy-candidate-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: var(--modern-weight-semibold);
  margin-bottom: var(--modern-space-2);
}

.modern-policy-target-card {
  padding: var(--modern-space-2) var(--modern-space-3);
  background: var(--modern-surface);
  border-radius: var(--modern-radius-panel);
  margin-top: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
}

.modern-policy-target-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.modern-policy-target-model {
  font-weight: var(--modern-weight-medium);
}

.modern-policy-target-badges {
  display: flex;
  gap: var(--modern-space-2);
}

.modern-policy-rules-block {
  margin-top: var(--modern-space-2);
  padding-top: var(--modern-space-2);
  border-top: var(--modern-line-width) dashed var(--modern-border);
}

.modern-policy-rules-heading {
  font-size: var(--modern-font-size-caption);
  color: var(--modern-muted);
  margin-bottom: var(--modern-space-1);
}

.modern-policy-rule-item {
  font-size: var(--modern-font-size-caption);
  padding: var(--modern-space-1) 0;
}

.modern-policy-rule-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.modern-policy-rule-name {
  font-weight: var(--modern-weight-medium);
}

.modern-policy-rule-badges {
  display: flex;
  gap: var(--modern-space-1);
}

.modern-policy-rule-detail {
  display: flex;
  gap: var(--modern-space-2);
  color: var(--modern-muted);
  margin-top: var(--modern-space-1);
}

.modern-policy-rule-action {
  font-family: var(--modern-font-mono);
}

.modern-policy-reference-details {
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  padding: var(--modern-space-2) var(--modern-space-3);
  background: var(--modern-surface);
}

.modern-policy-reference-summary {
  cursor: pointer;
  font-weight: var(--modern-weight-medium);
  color: var(--modern-text);
}

.modern-policy-reference-content {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-3);
  margin-top: var(--modern-space-3);
  padding-top: var(--modern-space-2);
  border-top: var(--modern-line-width) dashed var(--modern-border);
}

.modern-policy-reference-section {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-1);
}

.modern-policy-reference-heading {
  font-weight: var(--modern-weight-semibold);
  font-size: var(--modern-font-size-caption);
  text-transform: uppercase;
  color: var(--modern-muted);
}

.modern-policy-param-table {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-2);
}

.modern-policy-param-row {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-1);
  padding: var(--modern-space-1) var(--modern-space-2);
  background: var(--modern-subtle);
  border-radius: var(--modern-radius-panel);
}

.modern-policy-param-key {
  font-weight: var(--modern-weight-medium);
  color: var(--modern-text);
}

.modern-policy-param-meta {
  display: flex;
  flex-direction: column;
  gap: var(--modern-space-1);
}

.modern-policy-param-desc {
  color: var(--modern-muted);
}

.modern-policy-param-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-caption);
}

.modern-policy-param-tag {
  color: var(--modern-muted);
}
</style>
