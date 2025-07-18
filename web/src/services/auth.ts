import http from "@/utils/http";
import { useState } from "@/utils/state";

const AUTH_KEY = "authKey";

export const useAuthKey = () => {
  return useState<string | null>(AUTH_KEY, () => null);
};

export function useAuthService() {
  const authKey = useAuthKey();

  const login = async (key: string): Promise<boolean> => {
    try {
      await http.post("/auth/login", { auth_key: key });
      localStorage.setItem(AUTH_KEY, key);
      authKey.value = key;
      return true;
    } catch (_error) {
      // Error already logged
      return false;
    }
  };

  const logout = (): void => {
    localStorage.removeItem(AUTH_KEY);
    authKey.value = null;
  };

  const checkLogin = (): boolean => {
    if (authKey.value) {
      return true;
    }

    const key = localStorage.getItem(AUTH_KEY);
    if (key) {
      authKey.value = key;
    }
    return !!authKey.value;
  };

  return {
    login,
    logout,
    checkLogin,
  };
}
