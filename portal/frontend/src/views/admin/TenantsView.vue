<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface TenantNumber {
  id: number
  tenant_id: number
  number: string
  name: string
  header: string
  notify: string
}

interface Tenant {
  id: number
  name: string
  notify: string
  numbers: TenantNumber[]
}

interface TenantUser {
  id: number
  tenant_id: number
  username: string
  api_key: string
}

const tenants = ref<Tenant[]>([])
const error = ref('')
const busy = ref(false)
const expanded = ref(0) // tenant id whose detail panel is open
const users = ref<TenantUser[]>([])

// Create tenant form
const tForm = ref({ name: '', notify: '' })
// Edit tenant state
const tEdit = ref<Tenant | null>(null)
const tEditForm = ref({ name: '', notify: '' })

// Create user form (per expanded tenant)
const uForm = ref({ username: '', password: '', api_key: '' })
const newCreds = ref<{ username: string; password: string; api_key: string } | null>(null)

// Create number form (per expanded tenant)
const nForm = ref({ number: '', name: '', header: '', notify: '' })

async function load() {
  error.value = ''
  try {
    tenants.value = (await api<Tenant[]>('/admin/tenants')) || []
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function loadUsers(tid: number) {
  try {
    users.value = (await api<TenantUser[]>(`/admin/tenants/${tid}/users`)) || []
  } catch (e: any) { error.value = e.message }
}

async function expand(t: Tenant) {
  if (expanded.value === t.id) { expanded.value = 0; return }
  expanded.value = t.id
  newCreds.value = null
  uForm.value = { username: '', password: '', api_key: '' }
  nForm.value = { number: '', name: '', header: '', notify: '' }
  await loadUsers(t.id)
}

async function createTenant() {
  error.value = ''
  busy.value = true
  try {
    await api('/admin/tenants', { json: tForm.value })
    tForm.value = { name: '', notify: '' }
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

function startEditTenant(t: Tenant) {
  tEdit.value = t
  tEditForm.value = { name: t.name, notify: t.notify }
}

async function saveTenant() {
  if (!tEdit.value) return
  error.value = ''
  try {
    await api(`/admin/tenants/${tEdit.value.id}`, { method: 'PUT', json: tEditForm.value })
    tEdit.value = null
    await load()
  } catch (e: any) { error.value = e.message }
}

async function removeTenant(t: Tenant) {
  if (!confirm(`Delete tenant "${t.name}" (#${t.id})? Its numbers and users are NOT automatically removed upstream.`)) return
  error.value = ''
  try {
    await api(`/admin/tenants/${t.id}?confirm=true`, { method: 'DELETE' })
    if (expanded.value === t.id) expanded.value = 0
    await load()
  } catch (e: any) { error.value = e.message }
}

async function createUser(t: Tenant) {
  error.value = ''
  busy.value = true
  try {
    const res = await api<{ user: TenantUser; password: string; api_key: string }>(
      `/admin/tenants/${t.id}/users`, { json: uForm.value })
    newCreds.value = { username: res.user.username, password: res.password, api_key: res.api_key }
    uForm.value = { username: '', password: '', api_key: '' }
    await loadUsers(t.id)
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function resetUserPassword(u: TenantUser) {
  const p = prompt(`New password for ${u.username} (leave empty to cancel)`)
  if (!p) return
  error.value = ''
  try {
    await api(`/admin/tenant-users/${u.id}`, {
      method: 'PUT',
      json: { tenant_id: u.tenant_id, username: u.username, password: p, api_key: u.api_key },
    })
    await loadUsers(u.tenant_id)
  } catch (e: any) { error.value = e.message }
}

async function removeUser(u: TenantUser) {
  if (!confirm(`Delete user ${u.username}?`)) return
  error.value = ''
  try {
    await api(`/admin/tenant-users/${u.id}`, { method: 'DELETE' })
    await loadUsers(u.tenant_id)
  } catch (e: any) { error.value = e.message }
}

async function addNumber(t: Tenant) {
  error.value = ''
  busy.value = true
  try {
    await api(`/admin/tenants/${t.id}/numbers`, { json: nForm.value })
    nForm.value = { number: '', name: '', header: '', notify: '' }
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function editNumber(n: TenantNumber) {
  const name = prompt('Caller ID name', n.name)
  if (name === null) return
  const header = prompt('Fax header', n.header)
  if (header === null) return
  const notify = prompt('Notify list (email->a@b.c;…,webhook->…,portal->…)', n.notify)
  if (notify === null) return
  error.value = ''
  try {
    await api(`/admin/tenant-numbers/${n.id}`, {
      method: 'PUT',
      json: { tenant_id: n.tenant_id, number: n.number, name, header, notify },
    })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function removeNumber(n: TenantNumber) {
  if (!confirm(`Remove number ${n.number} from tenant #${n.tenant_id}?`)) return
  error.value = ''
  try {
    await api(`/admin/tenant-numbers/${n.id}?tenant_id=${n.tenant_id}&number=${encodeURIComponent(n.number)}&confirm=true`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Direct tenants <span class="muted">(gofaxserver tenants — live, no portal org required)</span></h2>
      <p class="muted">
        Tenants here exist only on gofaxserver: they authenticate to <code>/fax/send</code> with their
        own username/password (or API key) and receive faxes via their endpoints — no portal login,
        inbox, or org. Use this for direct integrations and imported databases.
      </p>
      <form class="inline" @submit.prevent="createTenant">
        <div><label>Name</label><input v-model="tForm.name" placeholder="Acme PBX" required /></div>
        <div><label>Notify</label><input v-model="tForm.notify" placeholder="email->ops@acme.tld (optional)" /></div>
        <button :disabled="busy">Create tenant</button>
      </form>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>ID</th><th>Name</th><th>Notify</th><th>Numbers</th><th></th></tr>
        </thead>
        <tbody>
          <template v-for="t in tenants" :key="t.id">
            <tr>
              <td>{{ t.id }}</td>
              <td>
                <template v-if="tEdit?.id === t.id">
                  <input v-model="tEditForm.name" style="width:160px" />
                </template>
                <template v-else>{{ t.name }}</template>
              </td>
              <td>
                <template v-if="tEdit?.id === t.id">
                  <input v-model="tEditForm.notify" style="width:220px" />
                </template>
                <template v-else>{{ t.notify || '—' }}</template>
              </td>
              <td>{{ t.numbers?.length || 0 }}</td>
              <td class="actions-cell">
                <template v-if="tEdit?.id === t.id">
                  <button class="secondary" @click="saveTenant">Save</button>
                  <button class="secondary" @click="tEdit = null">Cancel</button>
                </template>
                <template v-else>
                  <button class="secondary" @click="expand(t)">{{ expanded === t.id ? 'Close' : 'Manage' }}</button>
                  <button class="secondary" @click="startEditTenant(t)">Edit</button>
                  <button class="danger" @click="removeTenant(t)">Delete</button>
                </template>
              </td>
            </tr>
            <tr v-if="expanded === t.id">
              <td colspan="5">
                <div class="panel" style="margin:8px 0">
                  <h3>Auth users <span class="muted">(for /fax/send basic auth &amp; API key)</span></h3>
                  <table>
                    <thead>
                      <tr><th>ID</th><th>Username</th><th>API key</th><th></th></tr>
                    </thead>
                    <tbody>
                      <tr v-for="u in users" :key="u.id">
                        <td>{{ u.id }}</td>
                        <td>{{ u.username }}</td>
                        <td><code>{{ u.api_key }}</code></td>
                        <td class="actions-cell">
                          <button class="secondary" @click="resetUserPassword(u)">Reset password</button>
                          <button class="danger" @click="removeUser(u)">Delete</button>
                        </td>
                      </tr>
                      <tr v-if="!users.length"><td colspan="4" class="muted">No users yet.</td></tr>
                    </tbody>
                  </table>
                  <form class="inline" @submit.prevent="createUser(t)">
                    <div><label>Username</label><input v-model="uForm.username" required /></div>
                    <div><label>Password <span class="muted">(blank = generate)</span></label><input v-model="uForm.password" /></div>
                    <div><label>API key <span class="muted">(blank = generate)</span></label><input v-model="uForm.api_key" /></div>
                    <button :disabled="busy">Add user</button>
                  </form>
                  <p v-if="newCreds" class="muted">
                    Created <b>{{ newCreds.username }}</b> — password: <code>{{ newCreds.password }}</code> ·
                    API key: <code>{{ newCreds.api_key }}</code> (shown once, store it now)
                  </p>
                </div>

                <div class="panel" style="margin:8px 0">
                  <h3>Numbers <span class="muted">(assigned to this tenant)</span></h3>
                  <table>
                    <thead>
                      <tr><th>Number</th><th>Name</th><th>Header</th><th>Notify</th><th></th></tr>
                    </thead>
                    <tbody>
                      <tr v-for="n in t.numbers || []" :key="n.id">
                        <td>{{ n.number }}</td>
                        <td>{{ n.name || '—' }}</td>
                        <td>{{ n.header || '—' }}</td>
                        <td>{{ n.notify || '—' }}</td>
                        <td class="actions-cell">
                          <button class="secondary" @click="editNumber(n)">Edit</button>
                          <button class="danger" @click="removeNumber(n)">Remove</button>
                        </td>
                      </tr>
                      <tr v-if="!(t.numbers || []).length"><td colspan="5" class="muted">No numbers assigned.</td></tr>
                    </tbody>
                  </table>
                  <form class="inline" @submit.prevent="addNumber(t)">
                    <div><label>Number</label><input v-model="nForm.number" placeholder="15551234567" required /></div>
                    <div><label>Name</label><input v-model="nForm.name" placeholder="caller id name" /></div>
                    <div><label>Header</label><input v-model="nForm.header" placeholder="fax header" /></div>
                    <div><label>Notify</label><input v-model="nForm.notify" placeholder="email->…,webhook->…" /></div>
                    <button :disabled="busy">Assign number</button>
                  </form>
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="!tenants.length"><td colspan="5" class="muted">No tenants on gofaxserver yet.</td></tr>
        </tbody>
      </table>
    </div>
  </main>
</template>
