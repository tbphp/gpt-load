import Layout from "@/components/Layout.vue";
import { useAuthService } from "@/services/auth";
import http from "@/utils/http";
import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";

const routes: Array<RouteRecordRaw> = [
  {
    path: "/",
    component: Layout,
    children: [
      {
        path: "",
        name: "dashboard",
        component: () => import("@/views/Dashboard.vue"),
      },
      {
        path: "keys",
        name: "keys",
        component: () => import("@/views/Keys.vue"),
      },
      {
        path: "logs",
        name: "logs",
        component: () => import("@/views/Logs.vue"),
      },
      {
        path: "settings",
        name: "settings",
        component: () => import("@/views/Settings.vue"),
      },
    ],
  },
  {
    path: "/login",
    name: "login",
    component: () => import("@/views/Login.vue"),
  },
  {
    path: "/setup",
    name: "setup",
    component: () => import("@/views/Setup.vue"),
  },
];

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
});

const { checkLogin } = useAuthService();

router.beforeEach(async (to, _from, next) => {
  // 检查是否需要初始化
  if (to.path !== "/setup") {
    try {
      const response = await http.get("/setup/status");
      
      const { is_first_time_setup, setup_mode } = response as any;
      
      if (is_first_time_setup || setup_mode) {
        return next({ path: "/setup" });
      }
    } catch (error) {
      console.error("检查初始化状态失败:", error);
      // 如果无法检查状态，假设需要初始化
      return next({ path: "/setup" });
    }
  }

  // 如果在初始化页面且已完成初始化，则跳转到登录页
  if (to.path === "/setup") {
    try {
      const response = await http.get("/setup/status");
      
      const { is_first_time_setup, setup_mode } = response as any;
      
      if (!is_first_time_setup && !setup_mode) {
        return next({ path: "/login" });
      }
    } catch (error) {
      console.error("在初始化页面检查状态失败:", error);
      // 如果检查失败，保持在初始化页面
    }
  }

  // 常规登录检查
  const loggedIn = checkLogin();
  
  if (to.path !== "/login" && to.path !== "/setup" && !loggedIn) {
    return next({ path: "/login" });
  }

  if (to.path === "/login" && loggedIn) {
    return next({ path: "/" });
  }

  next();
});

export default router;
