<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

const route = useRoute()
const title = computed(() => String(route.meta.title ?? 'GPT-Load'))
</script>

<template>
  <main class="login-shell">
    <section class="surface-card login-card" :aria-labelledby="`${String(route.name)}-title`">
      <p class="eyebrow">M3 · Route skeleton</p>
      <h1 :id="`${String(route.name)}-title`">{{ title }}</h1>
      <p class="lead">页面路由已经接入；对应业务功能将在正式实施阶段完成。</p>
      <div class="meta-row">
        <RouterLink class="button-link" to="/">返回首页</RouterLink>
      </div>
    </section>
  </main>
</template>
