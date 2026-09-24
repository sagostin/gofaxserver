<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Org { id: number; name: string; active: boolean }
interface User {
  id: number
  username: string
  email: string
  role: string
  org_id: number | null
  org_name?: string
  active: boolean
  email_notify: boolean
  assigned_number_ids: number[]
}

const users = ref<User[]>([])
const orgs = ref<Org[]>([])
const error = ref('')
const busy = ref(false)
const form = ref({ username: '', email: '', password: '', role: 'user', org_id: 0, email_notify: true })

async function load() {
  try {
    users.value = await api<User[]>('/admin/users')
    orgs.value = await api<Org[]>('/admin/orgs')
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function create() {
  error.value = ''
  busy.value = true
  try {
    await api('/admin/users', { json: { ...form.value, org_id: form.value.role === 'admin' ? null : form.value.org_id } })
    form.value = { username: '', email: '', password: '', role: 'user', org_id: 0, email_notify: true }
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function toggleActive(u: User) {
  try { await api(`/admin/users/${u.id}`, { method: 'PUT', json: { active: !u.active } }); await load() }
  catch (e: any) { error.value = e.message }
}

async function toggleEmailNotify(u: User) {
  try { await api(`/admin/users/${u.id}`, { method: 'PUT', json: { email_notify: !u.email_notify } }); await load() }
  catch (e: any) { error.value = e.message }
}

async function editEmail(u: User) {
  const email = prompt(`Email for ${u.username} (used for fax receipt emails)`, u.email)
  if (email === null || email.trim() === u.email) return
  try { await api(`/admin/users/${u.id}`, { method: 'PUT', json: { email: email.trim() } }); await load() }
  catch (e: any) { error.value = e.message }
}

async function resetPw(u: User) {
  const pw = prompt(`New password for ${u.username} (min 10 chars)`)
  if (!pw) return
  try { await api(`/admin/users/${u.id}/password`, { json: { password: pw } }); alert('Password updated; their sessions were revoked.') }
  catch (e: any) { error.value = e.message }
}

async function remove(u: User) {
  if (!confirm(`Delete user ${u.username}?`)) return
  try { await api(`/admin/users/${u.id}`, { method: 'DELETE' }); await load() }
  catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Portal users</h2>
      <form class="inline" @submit.prevent="create">
        <div><label>Username</label><input v-model="form.username" required /></div>
        <div><label>Email (for receipts)</label><input v-model="form.email" type="email" required /></div>
        <div><label>Password</label><input v-model="form.password" type="password" minlength="10" required /></div>
        <div>
          <label>Role</label>
          <select v-model="form.role"><option value="user">Fax user</option><option value="admin">Admin</option></select>
        </div>
        <div v-if="form.role === 'user'">
          <label>Organization</label>
          <select v-model="form.org_id" required>
            <option :value="0" disabled>Select…</option>
            <option v-for="o in orgs" :key="o.id" :value="o.id">{{ o.name }}</option>
          </select>
        </div>
        <div v-if="form.role === 'user'">
          <label><input type="checkbox" v-model="form.email_notify" /> Fax receipt emails</label>
        </div>
        <button :disabled="busy">Create user</button>
      </form>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>Username</th><th>Email</th><th>Role</th><th>Org</th><th>Numbers</th><th>Receipts</th><th>Status</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td>{{ u.username }}</td>
            <td>{{ u.email }}</td>
            <td>{{ u.role }}</td>
            <td>{{ u.org_name || '—' }}</td>
            <td>{{ u.assigned_number_ids?.length || 0 }}</td>
            <td><span v-if="u.role === 'user'" class="badge" :class="u.email_notify ? 'active' : 'inactive'">{{ u.email_notify ? 'on' : 'off' }}</span><span v-else class="muted">—</span></td>
            <td><span class="badge" :class="u.active ? 'active' : 'inactive'">{{ u.active ? 'active' : 'inactive' }}</span></td>
            <td class="actions-cell">
              <button class="secondary" @click="editEmail(u)">Edit email</button>
              <button v-if="u.role === 'user'" class="secondary" @click="toggleEmailNotify(u)">Receipts {{ u.email_notify ? 'off' : 'on' }}</button>
              <button class="secondary" @click="resetPw(u)">Reset PW</button>
              <button class="secondary" @click="toggleActive(u)">{{ u.active ? 'Deactivate' : 'Activate' }}</button>
              <button class="danger" @click="remove(u)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
      <p class="muted">Assign outbound numbers under the Numbers tab. Receipt emails go to assigned users with receipts on; users with receipts off keep portal access but get no emails. Email, receipt and status changes re-sync upstream automatically.</p>
    </div>
  </main>
</template>
