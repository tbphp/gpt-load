<script setup lang="ts">
import AppFooter from "@/components/AppFooter.vue";
import GlobalTaskProgressBar from "@/components/GlobalTaskProgressBar.vue";
import Logout from "@/components/Logout.vue";
import NavBar from "@/components/NavBar.vue";
import { useMediaQuery } from "@vueuse/core";
import { ref, watch } from "vue";

const isMenuOpen = ref(false);
const isMobile = useMediaQuery("(max-width: 768px)");

watch(isMobile, value => {
  if (!value) {
    isMenuOpen.value = false;
  }
});

const toggleMenu = () => {
  isMenuOpen.value = !isMenuOpen.value;
};
</script>

<template>
  <n-layout class="main-layout">
    <n-layout-header class="layout-header">
      <div class="header-content">
        <div class="header-brand">
          <div class="brand-icon">
            <img src="@/assets/logo.png" alt="" />
          </div>
          <h1 v-if="!isMobile" class="brand-title">GPT Load</h1>
        </div>

        <nav v-if="!isMobile" class="header-nav">
          <nav-bar />
        </nav>

        <div class="header-actions">
          <logout v-if="!isMobile" />
          <n-button v-else text @click="toggleMenu">
            <svg viewBox="0 0 24 24" width="24" height="24">
              <path fill="currentColor" d="M3,6H21V8H3V6M3,11H21V13H3V11M3,16H21V18H3V16Z" />
            </svg>
          </n-button>
        </div>
      </div>
    </n-layout-header>

    <n-drawer v-model:show="isMenuOpen" :width="240" placement="right">
      <n-drawer-content title="GPT Load" body-content-style="padding: 0;">
        <nav-bar mode="vertical" @close="isMenuOpen = false" />
        <div class="mobile-actions">
          <logout />
        </div>
      </n-drawer-content>
    </n-drawer>

    <n-layout-content class="layout-content">
      <div class="content-wrapper">
        <router-view v-slot="{ Component }">
          <transition name="fade" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </div>
    </n-layout-content>
    <app-footer />
  </n-layout>

  <!-- 全局任务进度条 -->
  <global-task-progress-bar />
</template>

<style scoped>
.main-layout {
  background: transparent;
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.layout-header {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(20px);
  border-bottom: 1px solid rgba(0, 0, 0, 0.08);
  box-shadow: var(--shadow-sm);
  position: sticky;
  top: 0;
  z-index: 100;
  padding: 0 12px;
}

.header-content {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  overflow-x: auto;
  max-width: 1200px;
  margin: 0 auto;
}

.header-brand {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.brand-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 35px;
  height: 35px;
  img {
    height: 100%;
    width: 100%;
  }
}

.brand-title {
  font-size: 1.4rem;
  font-weight: 700;
  background: var(--primary-gradient);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
  margin: 0;
  letter-spacing: -0.3px;
}

.header-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

.mobile-actions {
  padding: 12px;
  border-top: 1px solid rgba(0, 0, 0, 0.08);
}

.layout-content {
  flex: 1;
  overflow: auto;
  background: transparent;
  max-width: 1200px;
  margin: 0 auto;
  width: 100%;
}

.content-wrapper {
  padding: 16px;
  min-height: calc(100vh - 111px);
}

.layout-footer {
  background: transparent;
  padding: 0;
}
</style>
