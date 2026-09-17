<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { DialogRoot } from 'reka-ui'
import {
  ArrowUpRight,
  Clock3,
  KeyRound,
  Layers2,
  ScrollText,
  ShieldCheck,
  UserRound,
} from '@lucide/vue'
import type { HealthReport } from '@modern/api/health'
import type { GroupRow } from '@modern/api/groups'
import {
  AppBadge,
  AppButton,
  AppChannelIcon,
  AppCollectionState,
  AppDialogContent,
  AppDialogHeader,
  AppFormSection,
  AppIcon,
  AppProgressBar,
} from '@modern/components/ui'
import { formatCompactNumber } from '@modern/components/ui/format'
import { accessUSD } from '@modern/features/access-keys/access-key-display'
import {
  healthLogsLocation,
  healthManageLocation,
  healthTime,
  type HealthIssue,
} from './health-display'

const props = defineProps<{
  issue?: HealthIssue
  report: HealthReport
  groups: ReadonlyMap<number, GroupRow>
}>()
defineEmits<{ close: [] }>()
const { t, locale } = useI18n()
const count = (value: number) => formatCompactNumber(value, locale.value)
const name = computed(() => props.issue?.name ?? '')
const group = computed(() =>
  props.issue?.groupID ? props.groups.get(props.issue.groupID) : undefined,
)
const groupStatus = computed(() =>
  props.report.groups.find((row) => row.id === props.issue?.groupID),
)
const recent = computed(() => {
  const row = props.issue?.credential
  return row
    ? [
        { label: 'successes', value: row.successes },
        { label: 'problems', value: row.problems },
        { label: 'consecutive', value: row.consecutive },
      ]
    : []
})
function period(seconds: number): string {
  const unit =
    seconds % 86400 === 0
      ? 'days'
      : seconds % 3600 === 0
        ? 'hours'
        : seconds % 60 === 0
          ? 'minutes'
          : 'seconds'
  return t('health.' + unit, {
    count: count(seconds / { days: 86400, hours: 3600, minutes: 60, seconds: 1 }[unit]),
  })
}
</script>

<template>
  <DialogRoot :open="true" @update:open="!$event && $emit('close')">
    <AppDialogContent
      placement="editor"
      size="sheet"
      :title="t('health.details')"
      :description="t('health.detailDescription')"
    >
      <AppDialogHeader
        :title="t('health.details')"
        :close-label="t('ui.close')"
        @close="$emit('close')"
      />
      <div class="modern-health-detail-body">
        <template v-if="issue">
          <section class="modern-health-detail-summary" :data-tone="issue.severity">
            <div class="modern-health-detail-identity">
              <span class="modern-health-object-icon"
                ><AppIcon
                  :icon="
                    issue.identityType === 'group'
                      ? Layers2
                      : issue.identityType === 'access_key'
                        ? KeyRound
                        : UserRound
                  "
              /></span>
              <h3>{{ name }}</h3>
            </div>
            <AppBadge :tone="issue.severity" size="xs">{{
              t('health.kinds.' + issue.kind)
            }}</AppBadge>
            <p>{{ issue.reason }}</p>
            <span class="modern-health-detail-impact">{{ issue.impact }}</span>
          </section>
          <AppFormSection :title="t('health.recoverySection')">
            <div class="modern-health-recovery">
              <AppIcon :icon="Clock3" size="sm" /><strong>{{ issue.recovery }}</strong>
            </div>
            <dl class="modern-health-facts">
              <div v-if="issue.recoveryAt">
                <dt>{{ t(issue.kind === 'credit' ? 'health.expires' : 'health.nextTime') }}</dt>
                <dd>{{ healthTime(issue.recoveryAt, locale, true) }}</dd>
              </div>
              <div v-if="issue.groupID">
                <dt>{{ t('health.group') }}</dt>
                <dd class="modern-health-detail-group">
                  <AppChannelIcon
                    v-if="group"
                    :icon="group.channelIcon"
                    :mark="group.channelMark"
                    :name="group.channelName"
                    :tooltip="false"
                    size="sm"
                  />{{ issue.groupName }}
                </dd>
              </div>
            </dl>
            <AppProgressBar
              v-if="issue.remaining !== undefined"
              :label="t('health.remaining')"
              :value="issue.remaining"
              tone="warning"
              size="sm"
            />
          </AppFormSection>
          <AppFormSection
            v-if="issue.credential"
            :title="t('health.recentSection')"
            :description="t('health.recentWindow', { minutes: count(report.statsWindow / 60) })"
          >
            <div class="modern-health-recent">
              <div v-for="item in recent" :key="item.label">
                <span>{{ t('health.' + item.label) }}</span
                ><strong>{{ count(item.value) }}</strong>
              </div>
            </div>
            <dl class="modern-health-facts">
              <div>
                <dt>{{ t('health.statusCode') }}</dt>
                <dd>{{ issue.credential.statusCode ?? '—' }}</dd>
              </div>
              <div>
                <dt>{{ t('health.weight') }}</dt>
                <dd>{{ count(issue.credential.weight) }}</dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection
            v-if="issue.kind === 'group' && groupStatus"
            :title="t('health.overview')"
          >
            <dl class="modern-health-facts">
              <div>
                <dt>{{ t('health.available') }}</dt>
                <dd>{{ count(groupStatus.counts.available) }}</dd>
              </div>
              <div>
                <dt>{{ t('health.cooldown') }}</dt>
                <dd>{{ count(groupStatus.counts.cooldown) }}</dd>
              </div>
              <div>
                <dt>{{ t('health.isolated') }}</dt>
                <dd>{{ count(groupStatus.counts.blacklisted) }}</dd>
              </div>
            </dl>
          </AppFormSection>
          <AppFormSection v-if="issue.accessKey" :title="t('health.quotaSection')">
            <article
              v-for="rule in issue.accessKey.rules"
              :key="rule.id"
              class="modern-health-quota-rule"
            >
              <header>
                <strong>{{
                  t(rule.kind === 'total' ? 'health.totalQuota' : 'health.periodicQuota')
                }}</strong
                ><span v-if="rule.period">{{ period(rule.period) }}</span>
              </header>
              <dl class="modern-health-rule-amounts">
                <div>
                  <dt>{{ t('health.used') }}</dt>
                  <dd>{{ accessUSD(rule.used, locale) }}</dd>
                </div>
                <div>
                  <dt>{{ t('health.limit') }}</dt>
                  <dd>{{ accessUSD(rule.limit, locale) }}</dd>
                </div>
              </dl>
              <AppProgressBar :label="t('health.remaining')" :value="0" tone="danger" size="sm" />
              <p v-if="rule.endsAt">
                {{ t('health.nextTime') }} · {{ healthTime(rule.endsAt, locale, true) }}
              </p>
            </article>
          </AppFormSection>
        </template>
        <AppCollectionState
          v-else
          :icon="ShieldCheck"
          :title="t('health.resolved')"
          :description="t('health.resolvedHelp')"
        />
        <p class="modern-health-observed">
          {{ t('health.snapshot') }} · {{ healthTime(report.observedAt, locale, true) }}
        </p>
      </div>
      <footer class="modern-health-detail-footer">
        <template v-if="issue">
          <AppButton as-child
            ><RouterLink :to="healthLogsLocation(issue)"
              ><AppIcon :icon="ScrollText" size="sm" />{{ t('health.logs') }}</RouterLink
            ></AppButton
          >
          <AppButton as-child variant="primary"
            ><RouterLink :to="healthManageLocation(issue)"
              >{{ t('health.manage') }}<AppIcon :icon="ArrowUpRight" size="sm" /></RouterLink
          ></AppButton>
        </template>
        <AppButton v-else @click="$emit('close')">{{ t('ui.close') }}</AppButton>
      </footer>
    </AppDialogContent>
  </DialogRoot>
</template>

<style scoped>
.modern-health-detail-body {
  display: grid;
  align-content: start;
  gap: var(--modern-space-5);
  min-height: 0;
  flex: 1;
  overflow-y: auto;
  scrollbar-gutter: var(--modern-scrollbar-gutter);
  padding: var(--modern-space-5);
}
.modern-health-detail-summary {
  display: grid;
  justify-items: start;
  gap: var(--modern-space-2);
  padding: var(--modern-space-4);
  background: var(--modern-subtle);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-panel);
  border-left: var(--modern-space-1) solid var(--modern-warning);
}
.modern-health-detail-summary[data-tone='danger'] {
  border-left-color: var(--modern-danger);
}
.modern-health-detail-identity {
  display: flex;
  align-items: center;
  gap: var(--modern-space-3);
  min-width: 0;
  margin-bottom: var(--modern-space-1);
}
.modern-health-detail-identity h3 {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  overflow-wrap: anywhere;
}
.modern-health-object-icon {
  display: grid;
  place-items: center;
  flex: none;
  width: var(--modern-control-md);
  height: var(--modern-control-md);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  background: var(--modern-surface);
  color: var(--modern-muted);
}
.modern-health-detail-summary p {
  font-size: var(--modern-font-size-secondary);
}
.modern-health-detail-impact {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-health-facts {
  display: grid;
  gap: var(--modern-space-3);
  font-size: var(--modern-font-size-secondary);
}
.modern-health-facts > div {
  display: grid;
  grid-template-columns: minmax(100px, 1fr) minmax(0, 1.7fr);
  align-items: start;
  gap: var(--modern-space-3);
}
.modern-health-facts dt {
  color: var(--modern-muted);
}
.modern-health-facts dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
  font-variant-numeric: tabular-nums;
}
.modern-health-detail-group {
  display: flex;
  align-items: center;
  gap: var(--modern-space-1-5);
}
.modern-health-recovery {
  display: flex;
  gap: var(--modern-space-2);
  align-items: center;
  color: var(--modern-text);
  font-size: var(--modern-font-size-secondary);
}
.modern-health-recovery strong {
  font-weight: var(--modern-weight-medium);
}
.modern-health-recent {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  padding: var(--modern-space-3) 0;
  margin-bottom: var(--modern-space-2);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
}
.modern-health-recent > div {
  display: grid;
  gap: var(--modern-space-2);
  padding-inline: var(--modern-space-3);
}
.modern-health-recent > div + div {
  border-left: var(--modern-line-width) solid var(--modern-border);
}
.modern-health-recent span {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-health-recent strong {
  font-size: var(--modern-font-size-section);
  font-weight: var(--modern-weight-semibold);
  font-variant-numeric: tabular-nums;
}
.modern-health-quota-rule {
  display: grid;
  gap: var(--modern-space-3);
  border: var(--modern-line-width) solid var(--modern-border);
  border-radius: var(--modern-radius-control);
  padding: var(--modern-space-3);
}
.modern-health-quota-rule header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--modern-space-2);
  font-size: var(--modern-font-size-secondary);
}
.modern-health-quota-rule header strong {
  font-weight: var(--modern-weight-medium);
}
.modern-health-quota-rule header span,
.modern-health-quota-rule p {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-health-rule-amounts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--modern-space-3);
  font-size: var(--modern-font-size-secondary);
}
.modern-health-rule-amounts > div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--modern-space-2);
}
.modern-health-rule-amounts dt {
  color: var(--modern-muted);
}
.modern-health-rule-amounts dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
}
.modern-health-observed {
  color: var(--modern-muted);
  font-size: var(--modern-font-size-small);
}
.modern-health-detail-footer {
  display: flex;
  flex: none;
  justify-content: flex-end;
  gap: var(--modern-space-3);
  padding: var(--modern-space-4) var(--modern-space-5);
  border-top: var(--modern-line-width) solid var(--modern-border);
}
</style>
