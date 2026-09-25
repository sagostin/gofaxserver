<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../../api'

interface Org {
  id: number
  name: string
  gofax_tenant_id: number
  svc_username: string
  tenant_notify: string
  totp_required: boolean
  active: boolean
  user_count: number
  number_count: number
}

const route = useRoute()
const orgId = computed(() => Number(route.params.id))
const org = ref<Org | null>(null)
const error = ref('')

onMounted(async () => {
  try {
    const orgs = await api<Org[]>('/admin/orgs')
    org.value = orgs.find((o) => o.id === orgId.value) || null
    if (!org.value) error.value = 'Organization not found.'
  } catch (e: any) { error.value = e.message }
})
</script>

<template>
  <main class="page">
    <p class="breadcrumb"><RouterLink to="/admin">← Organizations</RouterLink></p>
    <p v-if="error" class="error">{{ error }}</p>
    <template v-if="org">
      <div class="panel">
        <h2 style="margin-bottom:6px">{{ org.name }}</h2>
        <span class="muted">
          Tenant #{{ org.gofax_tenant_id }} · service account <code>{{ org.svc_username }}</code>
        </span>
        <span style="margin-left:10px">
          <span class="badge" :class="org.active ? 'active' : 'inactive'">{{ org.active ? 'active' : 'inactive' }}</span>
          <span class="badge" :class="org.totp_required ? 'active' : 'inactive'" style="margin-left:6px">2FA {{ org.totp_required ? 'required' : 'off' }}</span>
        </span>
      </div>
      <nav class="tabs">
        <RouterLink :to="`/admin/orgs/${orgId}/users`">Users</RouterLink>
        <RouterLink :to="`/admin/orgs/${orgId}/numbers`">Numbers</RouterLink>
      </nav>
      <RouterView :key="route.fullPath" />
    </template>
  </main>
</template>
