import { useAuthService } from "@/services/auth";
import axios from "axios";
import { appState } from "./app-state";

// Define a list of API URLs that don't need to display loading indicator
const noLoadingUrls = ["/tasks/status"];

declare module "axios" {
  interface AxiosRequestConfig {
    hideMessage?: boolean;
  }
}

const http = axios.create({
  baseURL: "/api",
  timeout: 60000,
  headers: { "Content-Type": "application/json" },
});

// Request interceptor
http.interceptors.request.use(config => {
  // Check if the current request URL is in the blocked list
  if (config.url && !noLoadingUrls.includes(config.url)) {
    appState.loading = true;
  }
  const authKey = localStorage.getItem("authKey");
  if (authKey) {
    config.headers.Authorization = `Bearer ${authKey}`;
  }
  return config;
});

// Response interceptor
http.interceptors.response.use(
  response => {
    appState.loading = false;
    if (response.config.method !== "get" && !response.config.hideMessage) {
      window.$message.success(response.data.message ?? "Operation successful");
    }
    return response.data;
  },
  error => {
    appState.loading = false;
    if (error.response) {
      if (error.response.status === 401) {
        if (window.location.pathname !== "/login") {
          const { logout } = useAuthService();
          logout();
          window.location.href = "/login";
        }
      }
      window.$message.error(error.response.data?.message || `Request failed: ${error.response.status}`, {
        keepAliveOnHover: true,
        duration: 5000,
        closable: true,
      });
    } else if (error.request) {
      window.$message.error("Network error, please check your connection");
    } else {
      window.$message.error("Request configuration error");
    }
    return Promise.reject(error);
  }
);

export default http;
