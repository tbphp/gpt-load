<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <main class="login-shell">
    <section class="surface-card login-card" aria-labelledby="login-title">
      <p class="eyebrow">GPT-Load</p>
      <h1 id="login-title">登录</h1>
      <p class="lead">本阶段只建立登录路由；实际 AUTH_KEY 会话流程将在 M3 后续阶段实现。</p>
      <div class="meta-row">
        <RouterLink class="button-link" to="/">返回首页</RouterLink>
      </div>
    </section>
  </main>
</template>
