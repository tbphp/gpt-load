import Layout from "@/components/Layout.vue";
import { useAuthService } from "@/services/auth";
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
      {
        path: "token-pool",
        name: "token-pool",
        component: () => import("@/views/TokenPool.vue"),
      },
    ],
  },
  {
    path: "/login",
    name: "login",
    component: () => import("@/views/Login.vue"),
  },
];

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
});

const { checkLogin } = useAuthService();

router.beforeEach((to, _from, next) => {
  const loggedIn = checkLogin();
  if (to.path !== "/login" && !loggedIn) {
    return next({ path: "/login" });
  }

  if (to.path === "/login" && loggedIn) {
    return next({ path: "/" });
  }

  next();
});

export default router;
