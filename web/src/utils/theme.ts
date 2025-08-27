import { ref, watchEffect } from "vue";

type Theme = "light" | "dark";

const THEME_KEY = "app_theme";

// 优先从 localStorage 读取主题，否则根据系统偏好设置
const initialTheme = (): Theme => {
  const storedTheme = localStorage.getItem(THEME_KEY) as Theme | null;
  if (storedTheme) {
    return storedTheme;
  }
  return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
};

export const currentTheme = ref<Theme>(initialTheme());

export const toggleTheme = () => {
  currentTheme.value = currentTheme.value === "light" ? "dark" : "light";
};

// 监听主题变化，并更新 localStorage 和 <html> class
watchEffect(() => {
  localStorage.setItem(THEME_KEY, currentTheme.value);
  if (currentTheme.value === "dark") {
    document.documentElement.classList.add("dark");
  } else {
    document.documentElement.classList.remove("dark");
  }
});
