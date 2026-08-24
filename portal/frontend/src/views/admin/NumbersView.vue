<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

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
  assigned_count: number
}
interface User { id: number; username: string; role: string; active: boolean; org_id: number | null }

const numbers = ref<NumberRow[]>([])
const orgs = ref<Org[]>([])
const users = ref<User[]>([])
const error = ref('')
const busy = ref(false)

const form = ref({ org_id: 0, number: '', name: '', header: '' })
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
    form.value = { org_id: 0, number: '', name: '', header: '' }
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function edit(n: NumberRow) {
  const name = prompt('Caller ID name', n.name)
  if (name === null) return
  const header = prompt('Fax header text', n.header)
  if (header === null) return
  error.value = ''
  try {
    await api(`/admin/numbers/${n.id}`, { method: 'PUT', json: { name, header } })
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
        <button :disabled="busy">Add number</button>
      </form>
      <p class="muted">Receipt emails are derived from assigned users and pushed to gofaxserver's notify field.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>Number</th><th>Org</th><th>Name / Header</th><th>Upstream</th><th>Status</th><th>Assigned</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="n in numbers" :key="n.id">
            <td><strong>{{ n.number }}</strong></td>
            <td>{{ n.org_name }}</td>
            <td>{{ n.name || '—' }} / {{ n.header || '—' }}</td>
            <td class="muted">#{{ n.gofax_number_id }}</td>
            <td><span class="badge" :class="n.active ? 'active' : 'inactive'">{{ n.active ? 'active' : 'inactive' }}</span></td>
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
  </main>
</template>
