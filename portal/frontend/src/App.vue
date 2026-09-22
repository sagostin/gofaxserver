<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from './auth'

const auth = useAuthStore()
const route = useRoute()
const links = computed(() => {
  if (!auth.me) return []
  if (auth.me.role === 'admin') {
    return [
      { to: '/admin', label: 'Orgs' },
      { to: '/admin/numbers', label: 'Numbers' },
      { to: '/admin/users', label: 'Users' },
      { to: '/admin/endpoints', label: 'Endpoints' },
      { to: '/admin/gateways', label: 'Gateways' },
      { to: '/admin/templates', label: 'Templates' },
      { to: '/admin/dialplan', label: 'Dialplan' },
      { to: '/admin/active', label: 'Active Faxes' },
      { to: '/admin/jobs', label: 'All Jobs' },
      { to: '/admin/audit', label: 'Audit' },
    ]
  }
  return [
    { to: '/app', label: 'Send Fax' },
    { to: '/app/faxes', label: 'My Faxes' },
  ]
})
</script>

<template>
  <div v-if="auth.me" class="topbar">
    <span class="brand">Fax Portal</span>
    <nav>
      <RouterLink v-for="l in links" :key="l.to" :to="l.to">{{ l.label }}</RouterLink>
    </nav>
    <span class="spacer"></span>
    <span class="muted">{{ auth.me.username }} · {{ auth.me.org_name || auth.me.role }}</span>
    <button class="secondary" @click="auth.logout()">Log out</button>
  </div>
  <RouterView :key="route.fullPath" />
</template>
