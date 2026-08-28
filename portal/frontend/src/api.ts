// Tiny fetch wrapper for the portal API. Handles CSRF + JSON + auth redirects.
let csrf = ''

export function setCsrf(token: string) {
  csrf = token
}

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}

export async function api<T = any>(path: string, opts: any = {}): Promise<T> {
  const headers: Record<string, string> = { ...(opts.headers || {}) }
  let body = opts.body
  if (opts.json !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.json)
  }
  const method: string = opts.method || (body ? 'POST' : 'GET')
  if (!['GET', 'HEAD'].includes(method)) {
    headers['X-CSRF-Token'] = csrf
  }
  const res = await fetch('/portal/api' + path, { ...opts, method, body, headers })
  if (res.status === 401 && !path.startsWith('/auth')) {
    window.location.href = '/portal/login'
    throw new ApiError('session expired', 401)
  }
  const data = res.status === 204 ? null : await res.json().catch(() => null)
  if (!res.ok) {
    throw new ApiError((data && data.error) || `HTTP ${res.status}`, res.status)
  }
  return data as T
}

export async function upload<T = any>(path: string, form: FormData): Promise<T> {
  return api<T>(path, { method: 'POST', body: form })
}
