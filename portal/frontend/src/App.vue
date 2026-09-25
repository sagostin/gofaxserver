<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from './auth'
import { useBrandingStore } from './branding'

const auth = useAuthStore()
const branding = useBrandingStore()
const route = useRoute()

interface NavLink { to: string; label: string }

const configLinks: NavLink[] = [
  { to: '/admin/endpoints', label: 'Endpoints' },
  { to: '/admin/gateways', label: 'Gateways' },
  { to: '/admin/templates', label: 'Templates' },
  { to: '/admin/dialplan', label: 'Dialplan' },
  { to: '/admin/fax-policies', label: 'Fax Policies' },
  { to: '/admin/branding', label: 'Branding' },
]

const links = computed<NavLink[]>(() => {
  if (!auth.me) return []
  if (auth.me.role === 'admin') {
    return [
      { to: '/admin', label: 'Orgs' },
      { to: '/admin/tenants', label: 'Tenants' },
      { to: '/admin/active', label: 'Active Faxes' },
      { to: '/admin/jobs', label: 'All Jobs' },
      { to: '/admin/inbound', label: 'Inbound Faxes' },
      { to: '/admin/audit', label: 'Audit' },
    ]
  }
  return [
    { to: '/app', label: 'Send Fax' },
    { to: '/app/faxes', label: 'My Faxes' },
    { to: '/app/inbox', label: 'Inbox' },
  ]
})

const isAdmin = computed(() => auth.me?.role === 'admin')
const configActive = computed(() => configLinks.some((l) => route.path.startsWith(l.to)))

const configOpen = ref(false)
const configRoot = ref<HTMLElement | null>(null)

function onDocClick(e: MouseEvent) {
  if (configRoot.value && !configRoot.value.contains(e.target as Node)) configOpen.value = false
}
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') configOpen.value = false
}
watch(() => route.fullPath, () => { configOpen.value = false })
onMounted(() => {
  document.addEventListener('mousedown', onDocClick)
  document.addEventListener('keydown', onKey)
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocClick)
  document.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div v-if="auth.me" class="topbar">
    <span class="brand">
      <img v-if="branding.logoUrl" :src="branding.logoUrl" :alt="branding.name" class="brand-logo" />
      <span v-else>{{ branding.name }}</span>
    </span>
    <nav>
      <RouterLink v-for="l in links" :key="l.to" :to="l.to">{{ l.label }}</RouterLink>
      <div v-if="isAdmin" ref="configRoot" class="nav-dropdown">
        <button type="button" class="nav-dropdown-btn" :class="{ active: configActive }" @click="configOpen = !configOpen">
          Configure ▾
        </button>
        <div v-if="configOpen" class="menu">
          <RouterLink v-for="l in configLinks" :key="l.to" :to="l.to" class="menu-item">{{ l.label }}</RouterLink>
        </div>
      </div>
    </nav>
    <span class="spacer"></span>
    <span class="muted">{{ auth.me.username }} · {{ auth.me.org_name || auth.me.role }}</span>
    <button class="secondary" @click="auth.logout()">Log out</button>
  </div>
  <RouterView :key="route.fullPath" />
</template>
