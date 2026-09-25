import { defineStore } from 'pinia'
import { api, setCsrf } from './api'

export interface Me {
  id: number
  username: string
  email: string
  role: 'admin' | 'user'
  org_id: number | null
  org_name?: string
  totp_enabled?: boolean
  csrf_token?: string
  numbers?: { id: number; number: string; name: string; header: string }[]
}

// LoginResult is either a completed login (Me) or an MFA challenge:
// mfa='totp_required' (enter code) or mfa='enroll_required' (scan QR first).
export type LoginResult = Me | { mfa: 'totp_required' | 'enroll_required'; mfa_token: string }

export interface TOTPEnrollment {
  secret: string
  otpauth_url: string
  qr_png: string
}

export const useAuthStore = defineStore('auth', {
  state: () => ({ me: null as Me | null }),
  getters: {
    isAdmin: (s) => s.me?.role === 'admin',
    isUser: (s) => s.me?.role === 'user',
  },
  actions: {
    async login(username: string, password: string): Promise<LoginResult> {
      const data = await api<any>('/auth/login', { json: { username, password } })
      if (data.mfa) return data as LoginResult
      setCsrf(data.csrf_token || '')
      this.me = data
      return data
    },
    async fetchTOTPSetup(mfaToken: string): Promise<TOTPEnrollment> {
      return api<TOTPEnrollment>('/auth/totp/setup', { json: { mfa_token: mfaToken } })
    },
    async verifyTOTP(mfaToken: string, code: string): Promise<Me> {
      const data = await api<any>('/auth/totp/verify', { json: { mfa_token: mfaToken, code } })
      setCsrf(data.csrf_token || '')
      this.me = data
      return data
    },
    async fetchMe() {
      try {
        const data = await api<Me>('/auth/me')
        setCsrf(data.csrf_token || '')
        this.me = data
        return data
      } catch {
        this.me = null
        return null
      }
    },
    async logout() {
      try {
        await api('/auth/logout', { method: 'POST' })
      } finally {
        this.me = null
        window.location.href = '/portal/login'
      }
    },
  },
})
