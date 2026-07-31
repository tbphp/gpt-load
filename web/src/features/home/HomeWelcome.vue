<script setup lang="ts">
import { ArrowRight, KeyRound, ShieldCheck } from '@lucide/vue'
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
      <AppButton type="button" @click="router.push(importLocation())">
        <KeyRound :size="16" aria-hidden="true" />
        {{ t('home.ledger.importKeys') }}
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
        <ShieldCheck :size="15" aria-hidden="true" />
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
  gap: var(--space-8);
  border-bottom: 1px solid var(--color-border-control);
  padding-bottom: var(--space-5);
}
.home-welcome__header h1 {
  max-width: none;
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-lede);
  font-weight: 500;
  line-height: var(--line-compact);
}
.home-welcome__guide {
  padding-top: var(--space-7);
}
.home-welcome__description {
  max-width: 45rem;
  margin: 0 0 var(--space-6);
  color: var(--color-text-muted);
  line-height: var(--line-relaxed);
}
.home-welcome__guide-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-3);
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
  font-size: var(--text-meta);
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
  gap: var(--space-3);
  align-items: center;
  border-bottom: 1px solid var(--color-border-subtle);
  padding: var(--space-4) var(--space-1);
}
.home-welcome__step-number {
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-meta);
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
  margin-top: var(--space-1);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  line-height: var(--line-relaxed);
}
.home-welcome__steps svg {
  color: var(--color-text-faint);
}
.home-welcome__note {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  margin: var(--space-5) 0 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-control);
  background: var(--color-surface-sunken);
  color: var(--color-text-muted);
  padding: var(--space-3);
  font-size: var(--text-meta);
  line-height: var(--line-relaxed);
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
    gap: var(--space-4);
  }
  .home-welcome__guide-header {
    align-items: start;
    flex-direction: column;
    gap: var(--space-1);
  }
  .home-welcome__steps li {
    grid-template-columns: 34px minmax(0, 1fr);
  }
  .home-welcome__steps li > svg {
    display: none;
  }
}
</style>
