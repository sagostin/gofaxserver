import { defineStore } from 'pinia'
import { api } from './api'

export interface Branding {
  name: string
  accent_color: string
  has_logo: boolean
  has_favicon: boolean
  v: number
}

// applyBranding pushes branding into the DOM: document title, favicon link,
// and the --accent CSS variable (empty color keeps the built-in default).
function applyBranding(b: Branding, logoUrl: string, faviconUrl: string) {
  document.title = b.name
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (faviconUrl) {
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      document.head.appendChild(link)
    }
    link.href = faviconUrl
  } else if (link) {
    link.remove()
  }
  if (b.accent_color) {
    document.documentElement.style.setProperty('--accent', b.accent_color)
  } else {
    document.documentElement.style.removeProperty('--accent')
  }
}

export const useBrandingStore = defineStore('branding', {
  state: () => ({
    name: 'Fax Portal',
    accentColor: '',
    logoUrl: '',
    faviconUrl: '',
    loaded: false,
  }),
  actions: {
    async fetch() {
      try {
        const b = await api<Branding>('/branding')
        this.name = b.name
        this.accentColor = b.accent_color || ''
        this.logoUrl = b.has_logo ? `/portal/api/branding/logo?v=${b.v}` : ''
        this.faviconUrl = b.has_favicon ? `/portal/api/branding/favicon?v=${b.v}` : ''
        applyBranding(b, this.logoUrl, this.faviconUrl)
      } catch {
        // Branding must never block the app; defaults stay in place.
      } finally {
        this.loaded = true
      }
    },
  },
})
