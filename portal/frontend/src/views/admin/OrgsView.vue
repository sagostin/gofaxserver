<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Org {
  id: number
  name: string
  gofax_tenant_id: number
  svc_username: string
  active: boolean
  user_count: number
  number_count: number
}

const orgs = ref<Org[]>([])
const name = ref('')
const error = ref('')
const busy = ref(false)
const report = ref<any>(null)

async function load() {
  try { orgs.value = await api<Org[]>('/admin/orgs') } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function create() {
  error.value = ''
  busy.value = true
  try {
    await api('/admin/orgs', { json: { name: name.value } })
    name.value = ''
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function toggleActive(o: Org) {
  error.value = ''
  try { await api(`/admin/orgs/${o.id}`, { method: 'PUT', json: { active: !o.active } }); await load() }
  catch (e: any) { error.value = e.message }
}

async function purge(o: Org) {
  if (!confirm(`Purge ${o.name}? Deletes the upstream tenant, numbers and deactivates its portal users. This cannot be undone.`)) return
  error.value = ''
  try {
    await api(`/admin/orgs/${o.id}?purge=true`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function reconcile(o: Org) {
  error.value = ''
  report.value = null
  try { report.value = await api(`/admin/orgs/${o.id}/reconcile`) }
  catch (e: any) { error.value = e.message }
}

async function rotateCredentials(o: Org) {
  if (!confirm(`Rotate the gofaxserver service-account credentials for ${o.name}? A fresh password + API key are generated, pushed upstream, verified, then stored. Brief send failures are possible while in flight.`)) return
  error.value = ''
  try {
    const r = await api<any>(`/admin/orgs/${o.id}/credentials/rotate`, { method: 'POST' })
    alert(r.recreated_upstream_user
      ? 'Service account was missing upstream — recreated with fresh credentials.'
      : 'Service account credentials rotated and verified.')
  } catch (e: any) { error.value = e.message }
}

async function resyncNotify(o: Org) {
  error.value = ''
  report.value = null
  try { report.value = await api<any>(`/admin/orgs/${o.id}/notify/resync`, { method: 'POST' }) }
  catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Organizations</h2>
      <form class="inline" @submit.prevent="create">
        <div><label>New organization name</label><input v-model="name" required /></div>
        <button :disabled="busy">Create org</button>
        <span class="muted">Provisions a gofaxserver tenant + service account.</span>
      </form>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel" v-if="report">
      <h3>Report</h3>
      <pre style="background:#f3f4f6;padding:10px;border-radius:8px;overflow:auto">{{ JSON.stringify(report, null, 2) }}</pre>
      <button class="secondary" @click="report = null">Close</button>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>ID</th><th>Name</th><th>Tenant</th><th>Service acct</th><th>Users</th><th>Numbers</th><th>Status</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="o in orgs" :key="o.id">
            <td>{{ o.id }}</td>
            <td>{{ o.name }}</td>
            <td>#{{ o.gofax_tenant_id }}</td>
            <td class="muted">{{ o.svc_username }}</td>
            <td>{{ o.user_count }}</td>
            <td>{{ o.number_count }}</td>
            <td><span class="badge" :class="o.active ? 'active' : 'inactive'">{{ o.active ? 'active' : 'inactive' }}</span></td>
            <td class="actions-cell">
              <button class="secondary" @click="toggleActive(o)">{{ o.active ? 'Deactivate' : 'Reactivate' }}</button>
              <button class="secondary" @click="reconcile(o)">Reconcile</button>
              <button class="secondary" @click="resyncNotify(o)">Re-sync notify</button>
              <button class="secondary" @click="rotateCredentials(o)">Rotate credentials</button>
              <button class="danger" @click="purge(o)">Purge</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </main>
</template>
