export const API_BASE =
  process.env.NEXT_PUBLIC_API_BASE || "/admin/api";

export interface AdminUser {
  id?: string;
  username: string;
  email?: string;
  role: string;
}

export function getToken(): string {
  if (typeof window === "undefined") return "";
  return localStorage.getItem("cr_admin_token") || "";
}

export function setToken(t: string) {
  if (typeof window !== "undefined") localStorage.setItem("cr_admin_token", t);
}

export function clearToken() {
  if (typeof window !== "undefined") {
    localStorage.removeItem("cr_admin_token");
    localStorage.removeItem("cr_admin_user");
  }
}

export function getUser(): AdminUser | null {
  if (typeof window === "undefined") return null;
  const raw = localStorage.getItem("cr_admin_user");
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

export function setUser(u: AdminUser) {
  if (typeof window !== "undefined") {
    localStorage.setItem("cr_admin_user", JSON.stringify(u));
  }
}

async function req<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...opts,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${getToken()}`,
      ...(opts.headers || {}),
    },
  });
  if (res.status === 401) {
    if (typeof window !== "undefined") {
      clearToken();
      window.dispatchEvent(new Event("cr:auth-changed"));
    }
    throw new UnauthorizedError();
  }
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const j = await res.json();
      if (j.error) msg = j.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

export class UnauthorizedError extends Error {
  constructor() {
    super("unauthorized");
  }
}

export const api = {
  get: <T = any>(p: string) => req<T>(p),
  post: <T = any>(p: string, body?: any) =>
    req<T>(p, { method: "POST", body: body ? JSON.stringify(body) : undefined }),
  put: <T = any>(p: string, body?: any) =>
    req<T>(p, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),
  patch: <T = any>(p: string, body?: any) =>
    req<T>(p, { method: "PATCH", body: body ? JSON.stringify(body) : undefined }),
  del: <T = any>(p: string) => req<T>(p, { method: "DELETE" }),
  login: async (username: string, password: string): Promise<{ token: string; user: AdminUser }> => {
    const res = await fetch(`${API_BASE}/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    const data = await res.json();
    if (!res.ok) {
      throw new Error(data.error || `Login failed (HTTP ${res.status})`);
    }
    setToken(data.token);
    setUser(data.user);
    return data;
  },
  logout: async () => {
    try {
      await fetch(`${API_BASE}/auth/logout`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${getToken()}`,
        },
      });
    } catch {
      /* ignore */
    }
    clearToken();
  },
};

export function getRouterPanelUrl(router: { endpoint_path?: string; slug?: string; native_panel_url?: string }): string {
  if (typeof window === "undefined") return "";
  const token = getToken();
  let path = router.native_panel_url || "";

  if (path.startsWith("http://") || path.startsWith("https://")) {
    try {
      const u = new URL(path);
      path = u.pathname;
    } catch {
      path = "/dashboard";
    }
  }

  const endpoint = router.endpoint_path || `/${router.slug || ""}`;
  if (!path || path === "/" || path === "/dashboard") {
    path = `${endpoint}/dashboard`;
  } else if (!path.startsWith(endpoint)) {
    path = `${endpoint}${path.startsWith("/") ? path : `/${path}`}`;
  }

  const base = window.location.origin;
  const url = new URL(path, base);
  if (token) {
    url.searchParams.set("token", token);
  }
  return url.toString();
}
