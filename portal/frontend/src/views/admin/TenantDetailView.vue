<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../../api'
import Modal from '../../components/Modal.vue'
import NotifyRulesEditor from '../../components/NotifyRulesEditor.vue'
import ActionsMenu, { type ActionItem } from '../../components/ActionsMenu.vue'

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

const route = useRoute()
const tenantId = computed(() => Number(route.params.id))

const tenant = ref<Tenant | null>(null)
const orgTenantIDs = ref<Set<number>>(new Set()) // gofax tenant IDs claimed by portal orgs
const users = ref<TenantUser[]>([])
const error = ref('')
const busy = ref(false)
const notFound = ref(false)

const orgManaged = computed(() => orgTenantIDs.value.has(tenantId.value))

// Create user form
const uForm = ref({ username: '', password: '', api_key: '' })
const newCreds = ref<{ username: string; password: string; api_key: string } | null>(null)

// Create number form
const nForm = ref({ number: '', name: '', header: '', notify: '' })

async function load() {
  error.value = ''
  try {
    const tenants = (await api<Tenant[]>('/admin/tenants')) || []
    tenant.value = tenants.find((t) => t.id === tenantId.value) || null
    notFound.value = !tenant.value
    const orgs = (await api<{ gofax_tenant_id: number }[]>('/admin/orgs')) || []
    orgTenantIDs.value = new Set(orgs.map((o) => o.gofax_tenant_id))
    if (tenant.value) await loadUsers()
  } catch (e: any) { error.value = e.message }
}

async function loadUsers() {
  try {
    users.value = (await api<TenantUser[]>(`/admin/tenants/${tenantId.value}/users`)) || []
  } catch (e: any) { error.value = e.message }
}

onMounted(load)

async function createUser() {
  error.value = ''
  busy.value = true
  try {
    const res = await api<{ user: TenantUser; password: string; api_key: string }>(
      `/admin/tenants/${tenantId.value}/users`, { json: uForm.value })
    newCreds.value = { username: res.user.username, password: res.password, api_key: res.api_key }
    uForm.value = { username: '', password: '', api_key: '' }
    await loadUsers()
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
    await loadUsers()
  } catch (e: any) { error.value = e.message }
}

async function removeUser(u: TenantUser) {
  if (!confirm(`Delete user ${u.username}?`)) return
  error.value = ''
  try {
    await api(`/admin/tenant-users/${u.id}`, { method: 'DELETE' })
    await loadUsers()
  } catch (e: any) { error.value = e.message }
}

async function addNumber() {
  error.value = ''
  busy.value = true
  try {
    await api(`/admin/tenants/${tenantId.value}/numbers`, { json: nForm.value })
    nForm.value = { number: '', name: '', header: '', notify: '' }
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

// Edit number state
const nEdit = ref<TenantNumber | null>(null)
const nEditForm = ref({ name: '', header: '', notify: '' })

function editNumber(n: TenantNumber) {
  nEdit.value = n
  nEditForm.value = { name: n.name, header: n.header, notify: n.notify }
}

async function saveNumber() {
  if (!nEdit.value) return
  error.value = ''
  try {
    await api(`/admin/tenant-numbers/${nEdit.value.id}`, {
      method: 'PUT',
      json: { tenant_id: nEdit.value.tenant_id, number: nEdit.value.number, ...nEditForm.value },
    })
    nEdit.value = null
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

function userActions(u: TenantUser): ActionItem[] {
  return [
    { label: 'Reset password', onClick: () => resetUserPassword(u) },
    { label: 'Delete', danger: true, onClick: () => removeUser(u) },
  ]
}

function numberActions(n: TenantNumber): ActionItem[] {
  return [
    { label: 'Edit', onClick: () => editNumber(n) },
    { label: 'Remove', danger: true, onClick: () => removeNumber(n) },
  ]
}
</script>

<template>
  <main class="page">
    <p class="breadcrumb"><RouterLink to="/admin/tenants">← Direct tenants</RouterLink></p>
    <p v-if="error" class="error">{{ error }}</p>
    <p v-if="notFound" class="error">Tenant not found.</p>

    <template v-if="tenant">
      <div class="panel">
        <h2 style="margin-bottom:6px">
          {{ tenant.name }}
          <span class="muted" style="font-weight:400">#{{ tenant.id }}</span>
          <span v-if="orgManaged" class="badge active" title="Claimed by a portal organization">portal org</span>
        </h2>
        <span class="muted">Notify: {{ tenant.notify || '—' }}</span>
      </div>

      <div class="panel">
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
              <td class="actions-cell"><ActionsMenu :items="userActions(u)" /></td>
            </tr>
            <tr v-if="!users.length"><td colspan="4" class="muted">No users yet.</td></tr>
          </tbody>
        </table>
        <form class="inline" @submit.prevent="createUser">
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

      <div class="panel">
        <h3>Numbers <span class="muted">(assigned to this tenant)</span></h3>
        <p v-if="orgManaged" class="muted" style="font-size:12px">
          This tenant is claimed by a portal organization — number notify fields are re-synced from portal state (assigned users + portal status push + per-number custom rules). Direct edits to notify here will be overwritten; set custom rules via the organization's Numbers page instead.
        </p>
        <table>
          <thead>
            <tr><th>Number</th><th>Name</th><th>Header</th><th>Notify</th><th></th></tr>
          </thead>
          <tbody>
            <tr v-for="n in tenant.numbers || []" :key="n.id">
              <td>{{ n.number }}</td>
              <td>{{ n.name || '—' }}</td>
              <td>{{ n.header || '—' }}</td>
              <td>{{ n.notify || '—' }}</td>
              <td class="actions-cell"><ActionsMenu :items="numberActions(n)" /></td>
            </tr>
            <tr v-if="!(tenant.numbers || []).length"><td colspan="5" class="muted">No numbers assigned.</td></tr>
          </tbody>
        </table>
        <form class="inline" @submit.prevent="addNumber">
          <div><label>Number</label><input v-model="nForm.number" placeholder="15551234567" required /></div>
          <div><label>Name</label><input v-model="nForm.name" placeholder="caller id name" /></div>
          <div><label>Header</label><input v-model="nForm.header" placeholder="fax header" /></div>
          <div><label>Notify</label><input v-model="nForm.notify" placeholder="email->…,webhook->…" /></div>
          <button :disabled="busy">Assign number</button>
        </form>
      </div>
    </template>

    <Modal v-if="nEdit" :title="`Edit number ${nEdit.number}`" @close="nEdit = null">
      <div class="field">
        <label>Caller ID name</label>
        <input v-model="nEditForm.name" placeholder="Acme Corp" />
      </div>
      <div class="field">
        <label>Fax header</label>
        <input v-model="nEditForm.header" placeholder="Acme Corp Fax" />
      </div>
      <div class="field">
        <label>Notify rules</label>
        <NotifyRulesEditor v-model="nEditForm.notify" />
        <p v-if="orgManaged" class="muted" style="margin:6px 0 0">
          This tenant is claimed by a portal organization — number notify is re-synced from portal state
          (assigned users + portal status push + per-number custom rules). Direct edits here will be overwritten;
          set custom rules via the organization's Numbers page instead.
        </p>
      </div>
      <template #footer>
        <button class="secondary" @click="nEdit = null">Cancel</button>
        <button @click="saveNumber">Save</button>
      </template>
    </Modal>
  </main>
</template>
