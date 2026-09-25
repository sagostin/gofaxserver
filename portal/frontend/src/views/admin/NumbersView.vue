<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'
import Modal from '../../components/Modal.vue'
import NotifyRulesEditor from '../../components/NotifyRulesEditor.vue'

interface Org { id: number; name: string; active: boolean }
interface NumberRow {
  id: number
  number: string
  name: string
  header: string
  gofax_number_id: number
  org_id: number
  org_name: string
  active: boolean
  inbound_enabled: boolean
  assigned_count: number
  custom_notify: string
}
interface User { id: number; username: string; role: string; active: boolean; org_id: number | null }

const numbers = ref<NumberRow[]>([])
const orgs = ref<Org[]>([])
const users = ref<User[]>([])
const error = ref('')
const busy = ref(false)

const form = ref({ org_id: 0, number: '', name: '', header: '', custom_notify: '' })
const assignFor = ref<NumberRow | null>(null)
const assignSel = ref<number[]>([])

async function load() {
  try {
    numbers.value = await api<NumberRow[]>('/admin/numbers')
    orgs.value = await api<Org[]>('/admin/orgs')
    users.value = await api<User[]>('/admin/users')
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function create() {
  error.value = ''
  busy.value = true
  try {
    await api('/admin/numbers', { json: form.value })
    form.value = { org_id: 0, number: '', name: '', header: '', custom_notify: '' }
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function edit(n: NumberRow) {
  editFor.value = n
  editForm.value = { name: n.name, header: n.header, custom_notify: n.custom_notify }
}

const editFor = ref<NumberRow | null>(null)
const editForm = ref({ name: '', header: '', custom_notify: '' })

async function saveEdit() {
  if (!editFor.value) return
  error.value = ''
  try {
    await api(`/admin/numbers/${editFor.value.id}`, { method: 'PUT', json: { ...editForm.value } })
    editFor.value = null
    await load()
  } catch (e: any) { error.value = e.message }
}

async function remove(n: NumberRow) {
  if (!confirm(`Delete ${n.number}? Removes it upstream too.`)) return
  error.value = ''
  try {
    await api(`/admin/numbers/${n.id}`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function toggleInbound(n: NumberRow) {
  error.value = ''
  try {
    await api(`/admin/numbers/${n.id}`, { method: 'PUT', json: { inbound_enabled: !n.inbound_enabled } })
    await load()
  } catch (e: any) { error.value = e.message }
}

function openAssign(n: NumberRow) {
  assignFor.value = n
  const orgUsers = users.value.filter((u) => u.role === 'user' && u.active && u.org_id === n.org_id)
  assignSel.value = []
  // pre-check via assignments endpoint
  api<{ user_ids: number[] }>(`/admin/numbers/${n.id}/assignments`).then((r) => (assignSel.value = r.user_ids || []))
  void orgUsers
}

function toggleUser(id: number) {
  const i = assignSel.value.indexOf(id)
  if (i >= 0) assignSel.value.splice(i, 1)
  else assignSel.value.push(id)
}

async function saveAssign() {
  if (!assignFor.value) return
  error.value = ''
  try {
    await api(`/admin/numbers/${assignFor.value.id}/assignments`, { method: 'PUT', json: { user_ids: assignSel.value } })
    assignFor.value = null
    await load()
  } catch (e: any) { error.value = e.message }
}

function orgUsers(orgId: number) {
  return users.value.filter((u) => u.role === 'user' && u.active && u.org_id === orgId)
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Numbers</h2>
      <form class="inline" @submit.prevent="create">
        <div>
          <label>Organization</label>
          <select v-model="form.org_id" required>
            <option :value="0" disabled>Select…</option>
            <option v-for="o in orgs" :key="o.id" :value="o.id">{{ o.name }}</option>
          </select>
        </div>
        <div><label>Number</label><input v-model="form.number" placeholder="5551234567" required /></div>
        <div><label>Caller ID name</label><input v-model="form.name" /></div>
        <div><label>Fax header</label><input v-model="form.header" /></div>
        <div><label>Custom notify <span class="muted">(optional)</span></label><input v-model="form.custom_notify" placeholder="email_full->ops@acme.tld,webhook->…" /></div>
        <button :disabled="busy">Add number</button>
      </form>
      <p class="muted">Receipt emails are derived from assigned users and pushed to gofaxserver's notify field. Custom notify rules are stored on the number and appended on every re-sync, so they survive assignment changes.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>Number</th><th>Org</th><th>Name / Header</th><th>Upstream</th><th>Status</th><th>Inbox</th><th>Assigned</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="n in numbers" :key="n.id">
            <td><strong>{{ n.number }}</strong></td>
            <td>{{ n.org_name }}</td>
            <td>{{ n.name || '—' }} / {{ n.header || '—' }}
              <div v-if="n.custom_notify" class="muted" style="font-size:11px" :title="n.custom_notify">+custom notify</div>
            </td>
            <td class="muted">#{{ n.gofax_number_id }}</td>
            <td><span class="badge" :class="n.active ? 'active' : 'inactive'">{{ n.active ? 'active' : 'inactive' }}</span></td>
            <td>
              <input type="checkbox" :checked="n.inbound_enabled" title="Deliver received faxes to the portal inbox" @change="toggleInbound(n)" />
            </td>
            <td>{{ n.assigned_count }}</td>
            <td class="actions-cell">
              <button @click="openAssign(n)">Assign</button>
              <button class="secondary" @click="edit(n)">Edit</button>
              <button class="danger" @click="remove(n)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="assignFor" class="panel" style="position:relative">
      <h3>Assign users to {{ assignFor.number }}</h3>
      <p class="muted">Each assigned user can use this number as their outbound caller ID, and receives email receipts at their account address.</p>
      <table>
        <tbody>
          <tr v-for="u in orgUsers(assignFor.org_id)" :key="u.id">
            <td style="width:30px">
              <input type="checkbox" :checked="assignSel.includes(u.id)" @change="toggleUser(u.id)" />
            </td>
            <td>{{ u.username }}</td>
          </tr>
          <tr v-if="!orgUsers(assignFor.org_id).length"><td class="muted">No active fax users in this organization yet.</td></tr>
        </tbody>
      </table>
      <div style="margin-top:10px; display:flex; gap:8px">
        <button @click="saveAssign">Save assignments</button>
        <button class="secondary" @click="assignFor = null">Cancel</button>
      </div>
    </div>

    <Modal v-if="editFor" :title="`Edit ${editFor.number}`" @close="editFor = null">
      <div class="field">
        <label>Caller ID name</label>
        <input v-model="editForm.name" placeholder="Acme Corp" />
      </div>
      <div class="field">
        <label>Fax header text</label>
        <input v-model="editForm.header" placeholder="Acme Corp Fax" />
      </div>
      <div class="field">
        <label>Custom notify rules <span class="muted">(appended to the derived rules on every re-sync — they survive assignment changes)</span></label>
        <NotifyRulesEditor v-model="editForm.custom_notify" />
      </div>
      <template #footer>
        <button class="secondary" @click="editFor = null">Cancel</button>
        <button @click="saveEdit">Save</button>
      </template>
    </Modal>
  </main>
</template>
