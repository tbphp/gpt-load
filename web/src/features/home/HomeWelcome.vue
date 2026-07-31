<script setup lang="ts">
import { ArrowRight, KeyRound } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { importLocation } from '@/app/route-locations'
import AppButton from '@/components/ui/AppButton.vue'

const { t } = useI18n()
const router = useRouter()
</script>

<template>
  <section class="home-welcome" aria-labelledby="home-title">
    <div class="home-welcome__intro">
      <span class="home-welcome__mark" aria-hidden="true"><KeyRound :size="18" /></span>
      <p class="home-welcome__eyebrow">{{ t('home.ledger.welcomeEyebrow') }}</p>
      <h1 id="home-title">{{ t('home.ledger.welcomeTitle') }}</h1>
      <p class="home-welcome__description">{{ t('home.ledger.welcomeDescription') }}</p>
      <AppButton type="button" @click="router.push(importLocation())">
        <KeyRound :size="16" aria-hidden="true" />
        {{ t('home.importKeys') }}
        <ArrowRight :size="16" aria-hidden="true" />
      </AppButton>
    </div>
    <ol class="home-welcome__steps">
      <li v-for="step in 3" :key="step">
        <span aria-hidden="true">{{ step }}</span>
        <p>{{ t(`home.ledger.welcomeStep${step}`) }}</p>
      </li>
    </ol>
  </section>
</template>

<style scoped>
.home-welcome {
  display: grid;
  min-height: 420px;
  grid-template-columns: minmax(0, 1.1fr) minmax(260px, 0.9fr);
  gap: var(--space-8);
  align-items: center;
}
.home-welcome__intro {
  display: grid;
  justify-items: start;
  gap: var(--space-4);
}
.home-welcome__mark {
  display: grid;
  width: var(--control-lg);
  height: var(--control-lg);
  place-items: center;
  border-radius: var(--radius-control);
  background: var(--color-action-soft);
  color: var(--color-action);
}
.home-welcome__eyebrow {
  margin: 0;
  color: var(--color-text-faint);
  font-size: var(--text-meta);
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.home-welcome h1 {
  margin: 0;
  font-family: var(--font-serif);
  font-size: var(--title-login);
  font-weight: 500;
  line-height: var(--line-compact);
}
.home-welcome__description {
  max-width: 38rem;
  margin: 0;
  color: var(--color-text-muted);
  line-height: var(--line-relaxed);
}
.home-welcome__steps {
  display: grid;
  gap: var(--space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}
.home-welcome__steps li {
  display: grid;
  grid-template-columns: var(--control-sm) minmax(0, 1fr);
  gap: var(--space-3);
  align-items: start;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: var(--space-4);
}
.home-welcome__steps span {
  display: grid;
  width: var(--control-sm);
  height: var(--control-sm);
  place-items: center;
  border: 1px solid var(--color-border-control);
  border-radius: 50%;
  color: var(--color-text-faint);
  font-family: var(--font-mono);
  font-size: var(--text-meta);
}
.home-welcome__steps p {
  margin: 0;
  color: var(--color-text-muted);
  line-height: var(--line-normal);
}
@media (max-width: 860px) {
  .home-welcome {
    min-height: 420px;
    grid-template-columns: 1fr;
    align-content: center;
    gap: var(--space-8);
  }
}
</style>
