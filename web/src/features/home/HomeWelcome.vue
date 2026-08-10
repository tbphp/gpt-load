<script setup lang="ts">
import { ArrowRight, CircleCheck, KeyRound } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { importLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'

const { t } = useI18n()
const router = useRouter()
</script>

<template>
  <section class="home-welcome" aria-labelledby="home-title">
    <header class="home-welcome__header">
      <h1 id="home-title">{{ t('home.ledger.welcomeTitle') }}</h1>
      <AppButton
        class="home-welcome__action"
        type="button"
        size="cta"
        @click="router.push(importLocation())"
      >
        <KeyRound :size="16" aria-hidden="true" />
        {{ t('home.ledger.importCredentials') }}
      </AppButton>
    </header>

    <section class="home-welcome__guide" aria-labelledby="home-welcome-guide-title">
      <p class="home-welcome__description">{{ t('home.ledger.welcomeDescription') }}</p>
      <div class="home-welcome__guide-header">
        <h2 id="home-welcome-guide-title">{{ t('home.ledger.welcomeGuideTitle') }}</h2>
        <span>{{ t('home.ledger.welcomeEstimatedTime') }}</span>
      </div>
      <ol class="home-welcome__steps">
        <li v-for="step in 3" :key="step">
          <span class="home-welcome__step-number" aria-hidden="true">0{{ step }}</span>
          <div>
            <h3>{{ t(`home.ledger.welcomeStep${step}Title`) }}</h3>
            <p>{{ t(`home.ledger.welcomeStep${step}Description`) }}</p>
          </div>
          <ArrowRight :size="16" aria-hidden="true" />
        </li>
      </ol>
      <p class="home-welcome__note">
        <CircleCheck :size="15" aria-hidden="true" />
        {{ t('home.ledger.welcomeSecurityNote') }}
      </p>
    </section>
  </section>
</template>

<style scoped>
.home-welcome {
  min-width: 0;
}
.home-welcome__header {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  min-height: 72px;
  align-items: center;
  gap: 36px;
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}
.home-welcome__header h1 {
  max-width: none;
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  letter-spacing: -0.015em;
  line-height: var(--line-compact);
}

.home-welcome__action {
  white-space: nowrap;
}

.home-welcome__guide {
  padding-top: 28px;
}
.home-welcome__description {
  max-width: 45rem;
  margin: 0 0 24px;
  color: var(--color-text-muted);
  font-size: 14px;
  line-height: var(--line-relaxed);
}
.home-welcome__guide-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: 10px;
}
.home-welcome__guide-header h2 {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-section);
  font-weight: 500;
}
.home-welcome__guide-header span {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 11px;
}
.home-welcome__steps {
  display: grid;
  margin: 0;
  padding: 0;
  border-top: 1px solid var(--color-border-subtle);
  list-style: none;
}
.home-welcome__steps li {
  display: grid;
  grid-template-columns: 46px minmax(0, 1fr) auto;
  gap: 14px;
  align-items: center;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: 17px 4px;
}
.home-welcome__step-number {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: 11px;
}
.home-welcome__steps h3,
.home-welcome__steps p {
  margin: 0;
}
.home-welcome__steps h3 {
  font-size: var(--text-md);
  font-weight: 600;
}
.home-welcome__steps p {
  margin-top: 3px;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
  line-height: 1.6;
}
.home-welcome__steps svg {
  color: var(--color-text-faint);
}
.home-welcome__note {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  margin: 18px 0 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: 8px;
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: 11px 13px;
  font-size: var(--text-sm);
  line-height: 1.6;
}
.home-welcome__note svg {
  flex: none;
  margin-top: 1px;
  color: var(--color-success);
}
@media (max-width: 860px) {
  .home-welcome__header {
    grid-template-columns: 1fr;
    align-items: start;
  }
  .home-welcome__action {
    justify-self: start;
  }
}

@media (max-width: 560px) {
  .home-welcome__guide-header {
    align-items: start;
    flex-direction: column;
    gap: 3px;
  }
  .home-welcome__steps li {
    grid-template-columns: 34px minmax(0, 1fr);
  }
  .home-welcome__steps li > svg {
    display: none;
  }
}
</style>
