import type { ListParams, ListResponse } from "@/lib/types";

/** Same-origin API prefix. The Next.js server proxies it to the Go backend (see proxy.ts). */
export const API_BASE = "/api/v1";

/** Error raised for any non-2xx response. Mirrors the backend error envelope. */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly requestId?: string;

  constructor(status: number, code: string, message: string, requestId?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

// ---- CSRF token: kept in memory only (never localStorage), set by the auth context. ----
let csrfToken: string | null = null;
export function setCsrfToken(token: string | null) {
  csrfToken = token;
}
export function getCsrfToken() {
  return csrfToken;
}

// ---- 401 handling: the auth provider registers a handler that redirects to /login. ----
type UnauthorizedHandler = () => void;
let unauthorizedHandler: UnauthorizedHandler | null = null;
export function setUnauthorizedHandler(handler: UnauthorizedHandler | null) {
  unauthorizedHandler = handler;
}

export type QueryValue = string | number | boolean | undefined | null;

export function buildQuery(query?: Record<string, QueryValue>): string {
  if (!query) return "";
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === "") continue;
    usp.append(k, String(v));
  }
  const s = usp.toString();
  return s ? `?${s}` : "";
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE" | "HEAD";
  body?: unknown;
  query?: Record<string, QueryValue>;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  /** Do not trigger the global 401 → /login redirect (used by the auth bootstrap itself). */
  skipAuthRedirect?: boolean;
}

/** Parse the `{"error":{code,message,request_id}}` envelope (tolerating non-JSON bodies). */
export async function parseError(res: Response): Promise<ApiError> {
  const requestId = res.headers.get("X-Request-Id") ?? undefined;
  let code = statusToCode(res.status);
  let message = res.statusText || `Request failed with status ${res.status}`;
  try {
    const text = await res.text();
    if (text) {
      try {
        const data = JSON.parse(text) as { error?: { code?: string; message?: string; request_id?: string } };
        if (data && data.error) {
          code = data.error.code || code;
          message = data.error.message || message;
          return new ApiError(res.status, code, message, data.error.request_id ?? requestId);
        }
      } catch {
        // Non-JSON error page (e.g. proxy failure) – surface a short excerpt.
        message = text.length > 200 ? `${text.slice(0, 200)}…` : text;
      }
    }
  } catch {
    // ignore body read errors
  }
  if (res.status === 502 || res.status === 504) {
    message = "The CloudSim backend is not reachable.";
  }
  return new ApiError(res.status, code, message, requestId);
}

function statusToCode(status: number): string {
  switch (status) {
    case 400:
      return "ValidationError";
    case 401:
      return "Unauthenticated";
    case 403:
      return "AccessDenied";
    case 404:
      return "NotFound";
    case 409:
      return "Conflict";
    case 429:
      return "RateLimited";
    case 501:
      return "NotImplemented";
    case 503:
      return "EngineUnavailable";
    default:
      return "InternalError";
  }
}

export function isMutating(method: string) {
  return method !== "GET" && method !== "HEAD";
}

/** Build request headers (credentials are same-origin cookies; CSRF header on non-GET). */
export function requestHeaders(method: string, extra?: Record<string, string>, json = false): Headers {
  const headers = new Headers(extra);
  if (!headers.has("Accept")) headers.set("Accept", "application/json");
  if (json && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (isMutating(method) && csrfToken) headers.set("X-CSRF-Token", csrfToken);
  return headers;
}

/** Low-level call returning the raw Response (throws ApiError on non-2xx). */
export async function apiRaw(path: string, opts: RequestOptions & { rawBody?: BodyInit } = {}): Promise<Response> {
  const method = opts.method ?? "GET";
  const isJson = opts.body !== undefined && opts.rawBody === undefined;
  const headers = requestHeaders(method, opts.headers, isJson);
  const url = `${API_BASE}${path}${buildQuery(opts.query)}`;
  let res: Response;
  try {
    res = await fetch(url, {
      method,
      headers,
      credentials: "include",
      body: opts.rawBody ?? (isJson ? JSON.stringify(opts.body) : undefined),
      signal: opts.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    throw new ApiError(0, "NetworkError", "Unable to reach the CloudSim console server. Check your connection.");
  }
  if (!res.ok) {
    const error = await parseError(res);
    if (res.status === 401 && !opts.skipAuthRedirect && unauthorizedHandler) unauthorizedHandler();
    throw error;
  }
  return res;
}

/** JSON API call. Returns `undefined` for 204 / empty bodies. */
export async function api<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await apiRaw(path, opts);
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    return text as unknown as T;
  }
}

/**
 * Normalise list responses. The contract returns `{items,total,page,page_size}` for list endpoints,
 * but a few endpoints document a bare array (instance types, images, activities…). Both are accepted.
 */
export function toList<T>(data: unknown, params?: ListParams): ListResponse<T> {
  if (Array.isArray(data)) {
    return { items: data as T[], total: data.length, page: 1, page_size: data.length || 20 };
  }
  const d = (data ?? {}) as Partial<ListResponse<T>>;
  const items = Array.isArray(d.items) ? d.items : [];
  return {
    items,
    total: typeof d.total === "number" ? d.total : items.length,
    page: typeof d.page === "number" ? d.page : params?.page ?? 1,
    page_size: typeof d.page_size === "number" ? d.page_size : params?.page_size ?? 20,
  };
}

/** GET a paginated list endpoint with the standard list params plus filters. */
export async function apiList<T>(path: string, params: ListParams = {}, signal?: AbortSignal): Promise<ListResponse<T>> {
  const data = await api<unknown>(path, { query: params as Record<string, QueryValue>, signal });
  return toList<T>(data, params);
}

/** Fetch all items of a (small) list endpoint, e.g. for <select> options. */
export async function apiAll<T>(path: string, query: Record<string, QueryValue> = {}, signal?: AbortSignal): Promise<T[]> {
  const data = await api<unknown>(path, { query: { page_size: 200, ...query }, signal });
  return toList<T>(data).items;
}

/** Encode an S3 key for a path, keeping `/` separators. */
export function encodeKey(key: string): string {
  return key.split("/").map(encodeURIComponent).join("/");
}

export interface UploadProgress {
  loaded: number;
  total: number;
}

/**
 * multipart/form-data upload with progress (XMLHttpRequest – fetch has no upload progress).
 * Sends the CSRF header and same-origin cookies.
 */
export function uploadForm<T>(path: string, form: FormData, onProgress?: (p: UploadProgress) => void): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${API_BASE}${path}`);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Accept", "application/json");
    if (csrfToken) xhr.setRequestHeader("X-CSRF-Token", csrfToken);
    xhr.upload.onprogress = (e) => {
      if (onProgress && e.lengthComputable) onProgress({ loaded: e.loaded, total: e.total });
    };
    xhr.onerror = () => reject(new ApiError(0, "NetworkError", "Upload failed: network error"));
    xhr.onload = async () => {
      const res = new Response(xhr.responseText || null, {
        status: xhr.status,
        headers: { "X-Request-Id": xhr.getResponseHeader("X-Request-Id") ?? "" },
      });
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(xhr.responseText ? (JSON.parse(xhr.responseText) as T) : (undefined as T));
        } catch {
          resolve(undefined as T);
        }
      } else {
        const err = await parseError(res);
        if (xhr.status === 401 && unauthorizedHandler) unauthorizedHandler();
        reject(err);
      }
    };
    xhr.send(form);
  });
}

/** Human readable message for any thrown value. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
