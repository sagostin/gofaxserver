import { defineStore } from 'pinia'
import { api, setCsrf } from './api'

export interface Me {
  id: number
  username: string
  email: string
  role: 'admin' | 'user'
  org_id: number | null
  org_name?: string
  numbers?: { id: number; number: string; name: string; header: string }[]
}

export const useAuthStore = defineStore('auth', {
  state: () => ({ me: null as Me | null }),
  getters: {
    isAdmin: (s) => s.me?.role === 'admin',
    isUser: (s) => s.me?.role === 'user',
  },
  actions: {
    async login(username: string, password: string) {
      const data = await api<any>('/auth/login', { json: { username, password } })
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
