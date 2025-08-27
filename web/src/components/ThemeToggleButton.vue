<script setup lang="ts">
import { currentTheme, toggleTheme } from "@/utils/theme";
import { NButton } from "naive-ui";
</script>

<template>
  <n-button @click="toggleTheme" class="theme-toggle-btn" quaternary>
    <template #icon>
      <span v-if="currentTheme === 'light'">🌙</span>
      <span v-else>☀️</span>
    </template>
  </n-button>
</template>

<style scoped>
.theme-toggle-btn {
  font-size: 1.2rem;
  padding: 0 8px;
  background-color: transparent;
  border: none;
  cursor: pointer;
  color: var(--text-color);
  transition: color 0.2s ease-in-out;
}
</style>
