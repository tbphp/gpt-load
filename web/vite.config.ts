import vue from "@vitejs/plugin-vue";
import path from "path";
import { defineConfig, loadEnv } from "vite";

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // Load environment variables
  const env = loadEnv(mode, path.resolve(__dirname, "../"), "");

  return {
    plugins: [vue()],
    // Resolution configuration
    resolve: {
      // Configure path aliases
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    // Development server configuration
    server: {
      // Proxy configuration example
      proxy: {
        "/api": {
          target: env.VITE_API_BASE_URL || "http://127.0.0.1:3001",
          changeOrigin: true,
        },
      },
    },
    // Build configuration
    build: {
      outDir: "dist",
      assetsDir: "assets",
    },
  };
});
