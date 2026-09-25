<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'
import ActionsMenu, { type ActionItem } from '../ActionsMenu.vue'

interface User {
  id: number
  username: string
  email: string
  role: string
  org_id: number | null
  org_name?: string
  active: boolean
  email_notify: boolean
  totp_enabled: boolean
  assigned_number_ids: number[]
}

const props = defineProps<{ orgId: number }>()

const users = ref<User[]>([])
const error = ref('')
const busy = ref(false)
const form = ref({ username: '', email: '', password: '', email_notify: true })

async function load() {
  try {
    users.value = await api<User[]>(`/admin/users?org_id=${props.orgId}`)
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function create() {
  error.value = ''
  busy.value = true
  try {
    await api('/admin/users', { json: { ...form.value, role: 'user', org_id: props.orgId } })
    form.value = { username: '', email: '', password: '', email_notify: true }
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

async function resetTOTP(u: User) {
  if (!confirm(`Reset 2FA for ${u.username}? Their authenticator link is erased and sessions revoked. If their org requires 2FA they will re-enroll at next login.`)) return
  try { await api(`/admin/users/${u.id}/totp/reset`, { method: 'POST' }); await load() }
  catch (e: any) { error.value = e.message }
}

async function remove(u: User) {
  if (!confirm(`Delete user ${u.username}?`)) return
  try { await api(`/admin/users/${u.id}`, { method: 'DELETE' }); await load() }
  catch (e: any) { error.value = e.message }
}

function userActions(u: User): ActionItem[] {
  const items: ActionItem[] = [{ label: 'Edit email', onClick: () => editEmail(u) }]
  if (u.role === 'user') items.push({ label: `Receipts ${u.email_notify ? 'off' : 'on'}`, onClick: () => toggleEmailNotify(u) })
  items.push({ label: 'Reset password', onClick: () => resetPw(u) })
  if (u.totp_enabled) items.push({ label: 'Reset 2FA', onClick: () => resetTOTP(u) })
  items.push({ label: u.active ? 'Deactivate' : 'Activate', danger: u.active, onClick: () => toggleActive(u) })
  items.push({ label: 'Delete', danger: true, onClick: () => remove(u) })
  return items
}
</script>

<template>
  <div class="panel">
    <h2>Users</h2>
    <form class="inline" @submit.prevent="create">
      <div><label>Username</label><input v-model="form.username" required /></div>
      <div><label>Email (for receipts)</label><input v-model="form.email" type="email" required /></div>
      <div><label>Password</label><input v-model="form.password" type="password" minlength="10" required /></div>
      <div>
        <label>&nbsp;</label>
        <label style="font-size:14px;color:var(--text)"><input type="checkbox" v-model="form.email_notify" /> Fax receipt emails</label>
      </div>
      <button :disabled="busy">Create user</button>
    </form>
    <p v-if="error" class="error">{{ error }}</p>
  </div>

  <div class="panel">
    <table>
      <thead>
        <tr><th>Username</th><th>Email</th><th>Numbers</th><th>Receipts</th><th>2FA</th><th>Status</th><th></th></tr>
      </thead>
      <tbody>
        <tr v-for="u in users" :key="u.id">
          <td>{{ u.username }}</td>
          <td>{{ u.email }}</td>
          <td>{{ u.assigned_number_ids?.length || 0 }}</td>
          <td><span class="badge" :class="u.email_notify ? 'active' : 'inactive'">{{ u.email_notify ? 'on' : 'off' }}</span></td>
          <td><span class="badge" :class="u.totp_enabled ? 'active' : 'inactive'">{{ u.totp_enabled ? 'enrolled' : 'not enrolled' }}</span></td>
          <td><span class="badge" :class="u.active ? 'active' : 'inactive'">{{ u.active ? 'active' : 'inactive' }}</span></td>
          <td class="actions-cell"><ActionsMenu :items="userActions(u)" /></td>
        </tr>
        <tr v-if="!users.length"><td colspan="7" class="muted">No users in this organization yet.</td></tr>
      </tbody>
    </table>
    <p class="muted">Assign outbound numbers under the Numbers tab. Receipt emails go to assigned users with receipts on; users with receipts off keep portal access but get no emails. Email, receipt and status changes re-sync upstream automatically.</p>
  </div>
</template>
