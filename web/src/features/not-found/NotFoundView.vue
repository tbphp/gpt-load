<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { homeLocation } from '@/app/route-locations'
import LedgerSheet from '@/components/layout/LedgerSheet.vue'
import PageFrame from '@/components/layout/PageFrame.vue'
import AppButton from '@/components/ui/AppButton.vue'
import OverflowTooltip from '@/components/ui/OverflowTooltip.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()

function goBack(): void {
  if (window.history.length > 1) {
    router.back()
    return
  }
  void router.push(homeLocation())
}
</script>

<template>
  <PageFrame>
    <LedgerSheet as="article" class="not-found" :padded="false">
      <aside class="not-found__code">
        <div>
          <strong>404</strong>
          <p>ROUTE_NOT_FOUND<br />MANAGEMENT_PLANE</p>
        </div>
        <span>{{ t('common.appName') }} · Console</span>
      </aside>

      <section class="not-found__content" aria-labelledby="not-found-title">
        <p class="not-found__eyebrow">{{ t('notFound.eyebrow') }}</p>
        <h1 id="not-found-title" tabindex="-1">{{ t('notFound.title') }}</h1>
        <p class="not-found__description">{{ t('notFound.description') }}</p>

        <div class="not-found__path">
          <span>{{ t('notFound.requestedPath') }}</span>
          <OverflowTooltip as="code" :content="route.path">{{ route.path }}</OverflowTooltip>
        </div>

        <div class="not-found__actions">
          <RouterLink class="button-link" :to="homeLocation()">
            {{ t('notFound.backHome') }}
          </RouterLink>
          <AppButton variant="secondary" @click="goBack">
            {{ t('notFound.backPrevious') }}
          </AppButton>
        </div>
      </section>
    </LedgerSheet>
  </PageFrame>
</template>

<style scoped>
.not-found {
  display: grid;
  min-height: calc(
    100vh - var(--topbar-height) - var(--stage-padding-top) - var(--stage-padding-bottom)
  );
  min-height: calc(
    100dvh - var(--topbar-height) - var(--stage-padding-top) - var(--stage-padding-bottom)
  );
  grid-template-columns: 230px minmax(0, 1fr);
  align-items: stretch;
  overflow: hidden;
}

.not-found__code {
  display: flex;
  height: 100%;
  flex-direction: column;
  justify-content: space-between;
  border-right: 1px solid var(--color-border-subtle);
  background: var(--color-surface-sunken);
  padding: 30px;
}

.not-found__code strong {
  font-family: var(--font-serif);
  font-size: 72px;
  font-weight: 500;
  letter-spacing: -0.07em;
  line-height: 1;
}

.not-found__code p {
  margin: 12px 0 0;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  line-height: var(--line-relaxed);
}

.not-found__code span {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 9.5px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.not-found__content {
  display: flex;
  width: 100%;
  max-width: 650px;
  flex-direction: column;
  justify-content: center;
  padding: 48px 56px;
}

.not-found__eyebrow {
  margin: 0 0 6px;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-label-xs);
  letter-spacing: 0.075em;
  text-transform: uppercase;
}

.not-found__content h1 {
  max-width: none;
  margin: 0;
  font-size: 24px;
  font-weight: 680;
  letter-spacing: -0.025em;
  line-height: 1.25;
}

.not-found__description {
  max-width: 560px;
  margin: 10px 0 0;
  color: var(--color-text-muted);
  line-height: 1.75;
}

.not-found__path {
  display: flex;
  max-width: 100%;
  align-items: center;
  gap: 9px;
  margin-top: 20px;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  padding: 10px 12px;
}

.not-found__path span {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  white-space: nowrap;
}

.not-found__path code {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.not-found__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-6);
}

@media (max-width: 860px) {
  .not-found {
    min-height: calc(
      100vh - var(--topbar-height) - var(--stage-padding-top-compact) -
        var(--stage-padding-bottom-compact)
    );
    min-height: calc(
      100dvh - var(--topbar-height) - var(--stage-padding-top-compact) -
        var(--stage-padding-bottom-compact)
    );
  }
}

@media (max-width: 760px) {
  .not-found {
    grid-template-columns: 1fr;
    grid-template-rows: auto minmax(0, 1fr);
  }

  .not-found__code {
    height: auto;
    min-height: 140px;
    border-right: 0;
    border-bottom: 1px solid var(--color-border-subtle);
    padding: 22px;
  }

  .not-found__code strong {
    font-size: 48px;
  }

  .not-found__code p {
    display: none;
  }

  .not-found__content {
    max-width: none;
    padding: 32px 22px;
  }
}
</style>
