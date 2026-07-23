<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <div class="page-shell">
    <header class="topbar">
      <RouterLink class="brand" to="/" aria-label="GPT-Load 首页">
        <span class="brand-mark" aria-hidden="true"></span>
        <span>GPT-Load</span>
      </RouterLink>
      <nav class="nav" aria-label="主导航">
        <RouterLink class="nav-link" to="/">首页</RouterLink>
        <RouterLink class="nav-link" to="/access-keys">访问密钥</RouterLink>
        <RouterLink class="nav-link" to="/monitor">监控</RouterLink>
        <RouterLink class="nav-link" to="/settings">设置</RouterLink>
      </nav>
      <RouterLink class="button-link topbar-action" to="/import">导入密钥</RouterLink>
    </header>
    <main class="page-content">
      <section class="surface-card" aria-labelledby="home-title">
        <p class="eyebrow">M3 · Single binary UI</p>
        <h1 id="home-title">管理界面基础已就绪</h1>
        <p class="lead">
          Vue 路由、正式视觉 token
          与单二进制构建链路已接入。真实运行状态与管理能力将在后续阶段按正式交互契约逐步实现。
        </p>
        <div class="meta-row" aria-label="当前骨架能力">
          <span class="meta-tag">Vue 3</span>
          <span class="meta-tag">Vite</span>
          <span class="meta-tag">Go Embed</span>
        </div>
      </section>
    </main>
  </div>
</template>
