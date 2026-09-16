<script setup lang="ts">
import { dateFormatter } from '@modern/components/ui/intl-formatters'
import {
  ChevronDown,
  ChevronUp,
  FileJson,
  Link2,
  LoaderCircle,
  RotateCcw,
  Trash2,
  X,
} from '@lucide/vue'
import { computed, nextTick, onScopeDispose, ref, shallowReactive, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupChannel, ProxyOverride } from '@modern/api/group-create'
import {
  beginAuthorization,
  cancelCredentialStage,
  completeAuthorization,
  importCredentialFiles,
  type CredentialImportItem,
  type CredentialStage,
} from '@modern/api/credential-stages'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppFileButton,
  AppIcon,
  AppIconButton,
  AppNotice,
  AppTextArea,
} from '@modern/components/ui'
import { useLoadingActivity } from '@modern/components/ui/loading'
import { useApiClient } from '@shared/http/client-context'
import { ApiError } from '@shared/http/errors'
import { useCredentialStageStatus } from './use-credential-stage-status'
import SubscriptionAuthorization from './SubscriptionAuthorization.vue'

const props = defineProps<{
  channel: GroupChannel
  groupId?: number
  proxy?: ProxyOverride
  disabled?: boolean
  entryDisabled?: boolean
  error?: string
}>()
const modelStages = defineModel<CredentialStage[]>({ required: true })
// 多个请求可能在同一轮更新中返回，用本地同步快照合并，再同步给父级。
const stages = shallowRef(modelStages.value)
watch(
  modelStages,
  (value) => {
    stages.value = value
  },
  { flush: 'sync' },
)
watch(
  stages,
  (value) => {
    if (value !== modelStages.value) modelStages.value = value
  },
  { flush: 'sync' },
)
const emit = defineEmits<{ busy: [value: boolean]; dirty: [value: boolean] }>()
const { t, te, n, locale } = useI18n()
const client = useApiClient()
const pending = shallowReactive(new Map<string, AbortController>())
const notice = ref('')
const json = ref('')
const jsonOpen = ref(false)
const callbacks = ref<Record<string, string>>({})
const callbackErrors = ref<Record<string, string>>({})
const stageErrors = ref<Record<string, string>>({})
const reports = ref<{ name: string; items: CredentialImportItem[] }[]>([])
const reportRows = computed(() =>
  reports.value.map((report) => ({
    ...report,
    formats: [...new Set(report.items.flatMap((item) => (item.format ? [item.format] : [])))],
  })),
)
const prepared = new Map<string, string>()
const formatIcons = {
  cpa: 'cpa',
  codex: 'codex',
  'claude-code': 'claude',
  sub2api: 'sub2api',
} satisfies Record<NonNullable<CredentialImportItem['format']>, string>
const authorizationNumbers = ref<Record<string, number>>({})
const collapsed = ref(new Set<string>())
let nextAuthorizationNumber = 0
const region = ref<HTMLElement>()
const stageArticles = new Map<string, HTMLElement>()
let disposed = false
const status = useCredentialStageStatus(client, stages, (id) =>
  Boolean(props.disabled || stageBusy(id)),
)
const busy = computed(() => pending.size > 0)
useLoadingActivity(busy)
const inactive = computed(() => props.disabled)
const entryInactive = computed(
  () => inactive.value || props.entryDisabled || stages.value.length >= 1000,
)
const supportsLogin = computed(() =>
  props.channel.authorizationMethods.some((method) => method !== 'oauth_file'),
)
const supportsFiles = computed(() => props.channel.authorizationMethods.includes('oauth_file'))
const authorizations = computed(() =>
  stages.value.filter((stage) => authorizationNumbers.value[stage.id] !== undefined),
)
const accounts = computed(() =>
  stages.value.filter((stage) => authorizationNumbers.value[stage.id] === undefined),
)
function authenticating(stage: CredentialStage): boolean {
  return (
    ['pending_authorization', 'exchanging'].includes(stage.status) &&
    status.problem(stage.id) !== 'stopped'
  )
}
function stageBusy(id: string): boolean {
  return ['authorize', 'callback', 'remove'].some((action) => pending.has(`${action}:${id}`))
}
function toggleAuthorization(id: string): void {
  const next = new Set(collapsed.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  collapsed.value = next
}
const readyCount = computed(() => stages.value.filter((stage) => stage.status === 'ready').length)
watch(busy, (value) => emit('busy', value), { immediate: true, flush: 'sync' })
watch(
  () => Boolean(json.value || Object.values(callbacks.value).some(Boolean) || reports.value.length),
  (value) => emit('dirty', value),
  { immediate: true, flush: 'sync' },
)
watch(
  stages,
  (value) => {
    for (const stage of value) {
      if (
        ['ready', 'consumed'].includes(stage.status) &&
        authorizationNumbers.value[stage.id] !== undefined
      ) {
        const focused = stageArticles.get(stage.id)?.contains(document.activeElement)
        delete authorizationNumbers.value[stage.id]
        collapsed.value.delete(stage.id)
        if (focused)
          void nextTick(() => stageArticles.get(stage.id)?.focus({ preventScroll: true }))
      } else if (
        ['pending_authorization', 'exchanging', 'outcome_unknown'].includes(stage.status) &&
        authorizationNumbers.value[stage.id] === undefined
      ) {
        authorizationNumbers.value[stage.id] = ++nextAuthorizationNumber
      }
    }
    const live = new Set(value.map((stage) => stage.id))
    for (const id of Object.keys(authorizationNumbers.value))
      if (!live.has(id)) delete authorizationNumbers.value[id]
    for (const id of collapsed.value) if (!live.has(id)) collapsed.value.delete(id)
    for (const id of Object.keys(stageErrors.value)) if (!live.has(id)) delete stageErrors.value[id]
    const awaiting = new Set(
      value.filter((stage) => stage.status === 'pending_authorization').map((stage) => stage.id),
    )
    callbacks.value = Object.fromEntries(
      Object.entries(callbacks.value).filter(([id]) => awaiting.has(id)),
    )
    callbackErrors.value = Object.fromEntries(
      Object.entries(callbackErrors.value).filter(([id]) => awaiting.has(id)),
    )
  },
  { immediate: true },
)
function errorMessage(cause: unknown, fallback: string): string {
  if (cause instanceof ApiError) {
    const data = cause.data
    if (
      data &&
      typeof data === 'object' &&
      'import_error' in data &&
      typeof data.import_error === 'string'
    )
      return codeMessage(data.import_error)
    return cause.message || t(fallback)
  }
  return t(fallback)
}
function codeMessage(code?: string): string {
  const key = `subscriptions.errors.${code}`
  return code && te(key) ? t(key) : t('subscriptions.stageError')
}
async function run<T>(
  action: string,
  work: (signal: AbortSignal) => Promise<T>,
  apply?: (value: T) => void,
  failure?: (cause: unknown) => void,
  stageID?: string,
): Promise<T | undefined> {
  if (inactive.value || disposed || pending.has(action) || (stageID && stageBusy(stageID))) return
  if (stageID) delete stageErrors.value[stageID]
  else notice.value = ''
  const request = new AbortController()
  pending.set(action, request)
  try {
    if (stageID) status.cancelReads(stageID)
    request.signal.throwIfAborted()
    const result = await work(request.signal)
    if (!request.signal.aborted && !disposed) {
      apply?.(result)
      return result
    }
  } catch (cause) {
    if (!request.signal.aborted && !disposed) {
      if (failure) failure(cause)
      else {
        const message = errorMessage(
          cause,
          action === 'import' ? 'subscriptions.importUnknown' : 'subscriptions.requestFailed',
        )
        if (stageID) stageErrors.value[stageID] = message
        else notice.value = message
      }
    }
  } finally {
    if (pending.get(action) === request) pending.delete(action)
  }
}
async function authorize(replaceID?: string): Promise<void> {
  if (
    inactive.value ||
    props.entryDisabled ||
    !supportsLogin.value ||
    (!replaceID && stages.value.length >= 1000)
  )
    return
  const stage = await run(
    replaceID ? `authorize:${replaceID}` : 'authorize',
    async (signal) => {
      const previous = stages.value.find((item) => item.id === replaceID)
      if (previous?.status === 'exchanging') return
      if (previous && ['pending_authorization', 'ready'].includes(previous.status)) {
        try {
          await cancelCredentialStage(client, previous.id, signal)
        } catch {
          signal.throwIfAborted()
          notice.value = t('subscriptions.cancelFailed')
        }
        status.merge([{ ...previous, status: 'cancelled' }])
        await nextTick()
      }
      return beginAuthorization(client, props.channel.id, props.proxy, signal, props.groupId)
    },
    (stage) => {
      if (!stage) return
      authorizationNumbers.value[stage.id] =
        (replaceID && authorizationNumbers.value[replaceID]) || ++nextAuthorizationNumber
      if (replaceID) delete authorizationNumbers.value[replaceID]
      stages.value = [...stages.value.filter((item) => item.id !== replaceID), stage]
    },
    undefined,
    replaceID,
  )
  if (stage && !disposed) {
    await nextTick()
    stageArticles.get(stage.id)?.focus({ preventScroll: true })
    stageArticles.get(stage.id)?.scrollIntoView({ block: 'start' })
  }
}
function stageRef(id: string, element: unknown): void {
  if (element instanceof HTMLElement) stageArticles.set(id, element)
  else stageArticles.delete(id)
}
function updateCallback(id: string, value: string): void {
  callbacks.value[id] = value
  delete callbackErrors.value[id]
}
async function importFiles(files: File[], pasted = false): Promise<void> {
  if (entryInactive.value || pending.has('import') || !supportsFiles.value || !files.length) return
  const submittedJSON = json.value
  if (
    files.length > 1000 ||
    files.reduce((total, file) => total + file.size, 0) > 4 * 1024 * 1024
  ) {
    notice.value = t('subscriptions.fileLimit')
    return
  }
  const active = new Set(
    stages.value.filter((stage) => stage.status === 'ready').map((stage) => stage.id),
  )
  const preparedIDs = [...prepared].filter(([, id]) => active.has(id)).map(([id]) => id)
  await run(
    'import',
    (signal) =>
      importCredentialFiles(
        client,
        props.channel.id,
        files,
        props.proxy,
        preparedIDs,
        signal,
        props.groupId,
      ),
    (result) => {
      for (const item of result)
        if (item.importID && item.stage) prepared.set(item.importID, item.stage.id)
      status.merge(result.flatMap((item) => (item.stage ? [item.stage] : [])))
      reports.value = [
        ...reports.value,
        ...files.map((file, index) => ({
          name: pasted ? t('subscriptions.pasted') : file.name,
          items: result.filter((item) => item.fileIndex === index + 1),
        })),
      ]
      if (
        pasted &&
        json.value === submittedJSON &&
        result.every((item) => item.status === 'ready')
      ) {
        json.value = ''
        jsonOpen.value = false
      }
    },
  )
}
function importJSON(): void {
  if (json.value.trim())
    void importFiles(
      [new File([json.value.trim()], 'credentials.json', { type: 'application/json' })],
      true,
    )
}
async function callback(stage: CredentialStage): Promise<void> {
  if (inactive.value || stageBusy(stage.id) || stage.status !== 'pending_authorization') return
  const value = callbacks.value[stage.id]?.trim() ?? ''
  let url: URL
  try {
    url = new URL(value)
    if (
      !['http:', 'https:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.hash ||
      new TextEncoder().encode(value).length > 16 * 1024
    )
      throw new Error()
  } catch {
    callbackErrors.value[stage.id] = t('subscriptions.callbackInvalid')
    return
  }
  if (stage.redirectURI) {
    const expected = new URL(stage.redirectURI)
    if (url.origin !== expected.origin || url.pathname !== expected.pathname) {
      callbackErrors.value[stage.id] = t('subscriptions.callbackTarget', {
        address: stage.redirectURI,
      })
      return
    }
  }
  const states = url.searchParams.getAll('state')
  const codes = url.searchParams.getAll('code')
  const errors = url.searchParams.getAll('error')
  if (
    states.length !== 1 ||
    !states[0]?.trim() ||
    !(
      (codes.length === 1 && codes[0]?.trim() && errors.length === 0) ||
      (errors.length === 1 && errors[0]?.trim() && codes.length === 0)
    )
  ) {
    callbackErrors.value[stage.id] = t('subscriptions.callbackIncomplete')
    return
  }
  delete callbackErrors.value[stage.id]
  await run(
    `callback:${stage.id}`,
    (signal) => completeAuthorization(client, stage.id, value, signal),
    (result) => {
      delete callbacks.value[stage.id]
      status.confirm(result)
    },
    (cause) => {
      callbackErrors.value[stage.id] =
        cause instanceof ApiError && cause.code === 'AUTHORIZATION_STATE_INVALID'
          ? t('subscriptions.callbackSessionMismatch')
          : errorMessage(cause, 'subscriptions.callbackUnknown')
    },
    stage.id,
  )
}
async function remove(stage: CredentialStage): Promise<void> {
  if (inactive.value || stageBusy(stage.id) || stage.status === 'exchanging') return
  if (['pending_authorization', 'ready'].includes(stage.status)) {
    await run(
      `remove:${stage.id}`,
      async (signal) => {
        await cancelCredentialStage(client, stage.id, signal)
        return true
      },
      () => {
        stages.value = stages.value.filter((item) => item.id !== stage.id)
      },
      () => {
        notice.value = t('subscriptions.cancelFailed')
        stages.value = stages.value.filter((item) => item.id !== stage.id)
      },
      stage.id,
    )
    return
  }
  stages.value = stages.value.filter((item) => item.id !== stage.id)
}
function summary(items: CredentialImportItem[]): string {
  return t('subscriptions.importSummary', {
    ready: n(items.filter((item) => item.status === 'ready').length),
    skipped: n(items.filter((item) => item.status === 'skipped').length),
    failed: n(items.filter((item) => item.status === 'failed').length),
  })
}
function expires(time: number): string {
  return dateFormatter(locale.value, { hour: '2-digit', minute: '2-digit' }).format(time)
}
function remaining(time: number): string {
  const seconds = Math.max(0, Math.floor((time - status.now.value) / 1000))
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
function tone(stage: CredentialStage): 'success' | 'warning' | 'neutral' | 'danger' {
  if (stage.status === 'ready') return 'success'
  if (['pending_authorization', 'exchanging'].includes(stage.status)) return 'warning'
  return stage.status === 'consumed' ? 'neutral' : 'danger'
}
onScopeDispose(() => {
  disposed = true
  for (const request of pending.values()) request.abort()
  pending.clear()
  stageArticles.clear()
  json.value = ''
  callbacks.value = {}
  prepared.clear()
  emit('busy', false)
  emit('dirty', false)
})
defineExpose({
  focus: () => {
    region.value?.focus()
    region.value?.scrollIntoView({ block: 'nearest' })
  },
})
</script>

<template>
  <section
    ref="region"
    class="modern-subscription-stager"
    tabindex="-1"
    :aria-label="t('subscriptions.title')"
  >
    <div class="modern-subscription-heading modern-subscription-progress">
      <h3>{{ t('subscriptions.title') }}</h3>
    </div>
    <div class="modern-subscription-actions">
      <AppButton
        v-if="supportsLogin"
        :icon="Link2"
        variant="primary"
        size="sm"
        :disabled="entryInactive || pending.has('authorize')"
        :loading="pending.has('authorize')"
        @click="authorize()"
        >{{ readyCount ? t('subscriptions.addAnother') : t('subscriptions.login') }}</AppButton
      >
      <AppFileButton
        v-if="supportsFiles"
        :label="t('subscriptions.importFile')"
        size="sm"
        accept=".json,application/json"
        multiple
        :disabled="entryInactive || pending.has('import')"
        :loading="pending.has('import')"
        @select="importFiles"
      />
      <AppButton
        v-if="supportsFiles"
        variant="ghost"
        size="sm"
        :icon="jsonOpen ? ChevronUp : ChevronDown"
        :disabled="inactive"
        :aria-expanded="jsonOpen"
        @click="jsonOpen = !jsonOpen"
        >{{ t('subscriptions.pasteJSON') }}</AppButton
      >
    </div>
    <div v-if="jsonOpen" class="modern-subscription-json">
      <AppTextArea
        v-model="json"
        :label="t('subscriptions.pasteJSON')"
        label-hidden
        mono
        :rows="5"
        :disabled="inactive"
        autocomplete="off"
        spellcheck="false"
      />
      <div class="modern-subscription-stage-actions">
        <AppButton
          :disabled="entryInactive || pending.has('import') || !json.trim()"
          :loading="pending.has('import')"
          @click="importJSON"
          >{{ t('subscriptions.importText') }}</AppButton
        >
      </div>
    </div>
    <AppNotice v-if="notice" tone="warning">{{ notice }}</AppNotice>
    <AppNotice v-if="error" tone="danger">{{ error }}</AppNotice>
    <article
      v-for="active in authorizations"
      :key="active.id"
      :ref="(element) => stageRef(active.id, element)"
      tabindex="-1"
      class="modern-subscription-authorization-panel"
    >
      <div class="modern-subscription-heading">
        <div class="modern-subscription-identity">
          <h4>
            {{
              t('subscriptions.authorizationTask', {
                channel: channel.name,
                number: n(authorizationNumbers[active.id]!),
              })
            }}
          </h4>
          <span v-if="authenticating(active)" class="modern-subscription-auth-status" role="status">
            <AppIcon :icon="LoaderCircle" size="sm" class="modern-spin" />
            {{ t('subscriptions.authenticating') }}
          </span>
          <AppBadge v-else :tone="tone(active)" dot>{{
            t('subscriptions.status.' + active.status)
          }}</AppBadge>
        </div>
        <div class="modern-subscription-stage-actions">
          <AppIconButton
            :icon="collapsed.has(active.id) ? ChevronDown : ChevronUp"
            :label="
              t(
                collapsed.has(active.id)
                  ? 'subscriptions.expandAuthorization'
                  : 'subscriptions.collapseAuthorization',
              )
            "
            size="xs"
            :aria-expanded="!collapsed.has(active.id)"
            @click="toggleAuthorization(active.id)"
          />
          <AppIconButton
            :icon="X"
            :label="t('subscriptions.cancelAuthorization')"
            size="xs"
            :disabled="inactive || stageBusy(active.id) || active.status === 'exchanging'"
            :loading="pending.has('remove:' + active.id)"
            @click="remove(active)"
          />
        </div>
      </div>
      <AppNotice v-if="stageErrors[active.id]" tone="warning">{{
        stageErrors[active.id]
      }}</AppNotice>
      <AppNotice v-if="status.problem(active.id)" tone="warning">{{
        t(
          status.problem(active.id) === 'stopped'
            ? 'subscriptions.pollStopped'
            : 'subscriptions.pollRetrying',
        )
      }}</AppNotice>
      <template v-if="!collapsed.has(active.id)">
        <template v-if="active.status === 'pending_authorization'">
          <p class="modern-subscription-hint">
            {{ t('subscriptions.authorizationHelp', { time: remaining(active.expiresAt) }) }}
          </p>
          <SubscriptionAuthorization
            :stage="active"
            :model-value="callbacks[active.id] ?? ''"
            :error="callbackErrors[active.id]"
            :disabled="inactive || stageBusy(active.id)"
            :submitting="pending.has('callback:' + active.id)"
            @update:model-value="updateCallback(active.id, $event)"
            @submit="callback(active)"
            @restart="authorize(active.id)"
          />
        </template>
        <template v-else>
          <AppNotice :tone="active.status === 'exchanging' ? 'info' : 'warning'">
            {{
              active.errorCode
                ? codeMessage(active.errorCode)
                : t(
                    active.status === 'exchanging'
                      ? 'subscriptions.exchangingHelp'
                      : 'subscriptions.stageError',
                  )
            }}
          </AppNotice>
          <div class="modern-subscription-stage-actions">
            <AppButton
              v-if="active.status !== 'exchanging'"
              :icon="RotateCcw"
              variant="brand"
              size="sm"
              :disabled="inactive || entryDisabled || stageBusy(active.id)"
              :loading="pending.has('authorize:' + active.id)"
              @click="authorize(active.id)"
              >{{ t('subscriptions.restart') }}</AppButton
            >
          </div>
        </template>
      </template>
    </article>
    <section v-if="accounts.length" class="modern-subscription-accounts">
      <div class="modern-subscription-account-heading">
        <h4>{{ t('subscriptions.pendingAccounts') }}</h4>
        <AppBadge size="xs">{{ n(readyCount) }}</AppBadge>
      </div>
      <article
        v-for="stage in accounts"
        :key="stage.id"
        :ref="(element) => stageRef(stage.id, element)"
        tabindex="-1"
        class="modern-subscription-account"
      >
        <AppChannelIcon
          class="modern-subscription-account-icon"
          :icon="channel.icon"
          :mark="channel.mark"
          :name="channel.name"
        />
        <div class="modern-subscription-account-details">
          <div class="modern-subscription-identity">
            <strong>{{ stage.email || t('subscriptions.connectionType') }}</strong>
            <AppBadge :tone="tone(stage)" variant="plain" dot>{{
              t('subscriptions.status.' + stage.status)
            }}</AppBadge>
          </div>
          <p v-if="stage.status === 'ready'" class="modern-subscription-hint">
            {{ t('subscriptions.readyHelp', { time: expires(stage.expiresAt) }) }}
          </p>
          <p v-else-if="stage.errorCode" class="modern-subscription-hint">
            {{ codeMessage(stage.errorCode) }}
          </p>
        </div>
        <div class="modern-subscription-stage-actions">
          <AppIconButton
            v-if="
              supportsLogin &&
              ['failed', 'expired', 'cancelled', 'outcome_unknown'].includes(stage.status)
            "
            :icon="RotateCcw"
            :label="t('subscriptions.restart')"
            size="xs"
            :disabled="inactive || entryDisabled || stageBusy(stage.id)"
            @click="authorize(stage.id)"
          />
          <AppIconButton
            :icon="Trash2"
            :label="t('subscriptions.remove')"
            size="xs"
            :disabled="inactive || stageBusy(stage.id) || stage.status === 'exchanging'"
            :loading="pending.has('remove:' + stage.id)"
            @click="remove(stage)"
          />
        </div>
      </article>
    </section>
    <div v-if="reports.length" class="modern-subscription-reports">
      <div class="modern-subscription-heading">
        <h4>{{ t('subscriptions.importResults') }}</h4>
        <AppIconButton
          :icon="X"
          :label="t('subscriptions.clearReports')"
          size="xs"
          :disabled="inactive"
          @click="reports = []"
        />
      </div>
      <div v-for="(report, index) in reportRows" :key="index" class="modern-subscription-report">
        <div v-if="report.formats.length" class="modern-subscription-report-icons">
          <AppChannelIcon
            v-for="format in report.formats"
            :key="format"
            class="modern-subscription-format-icon"
            :icon="formatIcons[format]"
            :name="t('subscriptions.formats.' + format)"
          />
        </div>
        <AppIcon v-else :icon="FileJson" />
        <div class="modern-subscription-report-content">
          <div class="modern-subscription-report-heading">
            <strong>{{ report.name }}</strong>
            <div class="modern-subscription-formats">
              <AppBadge v-for="format in report.formats" :key="format" size="xs">{{
                t('subscriptions.formats.' + format)
              }}</AppBadge>
            </div>
          </div>
          <p>{{ summary(report.items) }}</p>
          <details v-if="report.items.some((item) => item.status !== 'ready')">
            <summary>{{ t('subscriptions.viewIssues') }}</summary>
            <ul>
              <li
                v-for="item in report.items.filter((item) => item.status !== 'ready')"
                :key="item.index"
              >
                {{
                  t('subscriptions.importIssue', {
                    index: n(item.index),
                    message: codeMessage(item.errorCode),
                  })
                }}
              </li>
            </ul>
          </details>
        </div>
      </div>
    </div>
    <p class="modern-subscription-hint modern-subscription-risk">
      {{
        channel.notices.length
          ? channel.notices.map((id) => t('subscriptions.notices.' + id)).join(' ')
          : t('subscriptions.risk')
      }}
    </p>
  </section>
</template>

<style scoped>
.modern-subscription-stager,
.modern-subscription-json,
.modern-subscription-reports,
.modern-subscription-authorization-panel {
  display: grid;
  min-width: 0;
  gap: var(--modern-space-3);
}
.modern-subscription-heading,
.modern-subscription-account-heading,
.modern-subscription-actions,
.modern-subscription-report-icons,
.modern-subscription-report-heading,
.modern-subscription-identity,
.modern-subscription-stage-actions,
.modern-subscription-formats {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-subscription-heading {
  justify-content: space-between;
}
.modern-subscription-auth-status {
  display: inline-flex;
  align-items: center;
  gap: var(--modern-space-1-5);
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-subscription-stager {
  align-content: start;
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  padding: var(--modern-space-4);
  background: var(--modern-surface);
}
.modern-subscription-progress {
  position: relative;
  padding-bottom: var(--modern-space-2);
}
.modern-subscription-stager h3 {
  font-size: var(--modern-font-size-body);
  font-weight: var(--modern-weight-semibold);
}
.modern-subscription-stager h4,
.modern-subscription-identity strong {
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
  overflow-wrap: anywhere;
}
.modern-subscription-hint,
.modern-subscription-report {
  font-size: var(--modern-font-size-small);
  color: var(--modern-muted);
  line-height: var(--modern-leading-body);
}
.modern-subscription-format-icon,
.modern-subscription-account-icon {
  font-size: var(--modern-icon-md);
}
.modern-subscription-authorization-panel {
  border-top: var(--modern-line-width) solid var(--modern-border);
  padding-top: var(--modern-space-3);
}
.modern-subscription-accounts {
  min-width: 0;
  padding: var(--modern-space-3);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
}
.modern-subscription-account-heading {
  padding-bottom: var(--modern-space-2);
}
.modern-subscription-account {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  padding-top: var(--modern-space-3);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
.modern-subscription-account + .modern-subscription-account {
  margin-top: var(--modern-space-3);
}
.modern-subscription-risk {
  font-size: var(--modern-font-size-caption);
}
.modern-subscription-account-details {
  display: grid;
  flex: 1;
  min-width: 0;
  gap: var(--modern-space-1);
}
.modern-subscription-stage-actions {
  justify-content: flex-end;
  flex-shrink: 0;
}
.modern-subscription-report {
  display: flex;
  align-items: flex-start;
  gap: var(--modern-space-2);
  padding: var(--modern-space-3);
  border-radius: var(--modern-radius-control);
  background: var(--modern-subtle);
  overflow-wrap: anywhere;
}
.modern-subscription-report-content {
  display: grid;
  min-width: 0;
  flex: 1;
  gap: var(--modern-space-1);
}
.modern-subscription-report-icons {
  flex-shrink: 0;
}
.modern-subscription-report-heading strong {
  min-width: 0;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
  font-weight: var(--modern-weight-medium);
}
.modern-subscription-stager summary {
  cursor: pointer;
  color: var(--modern-muted);
  font-size: var(--modern-font-size-secondary);
}
.modern-subscription-stager details[open] > summary {
  margin-bottom: var(--modern-space-3);
}
.modern-subscription-stager ul {
  display: grid;
  gap: var(--modern-space-1);
  padding-left: var(--modern-space-4);
}
</style>
