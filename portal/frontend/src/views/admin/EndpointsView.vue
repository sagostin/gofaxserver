<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Ep {
  id: number
  type: string
  type_id: number
  endpoint_type: string
  endpoint: string
  priority: number
  bridge: boolean
}

const eps = ref<Ep[]>([])
const error = ref('')
const busy = ref(false)
const form = ref({ type: 'tenant', type_id: 0, endpoint_type: 'gateway', endpoint: '', priority: 0, bridge: false })
const confirmGlobal = ref(false)

async function load() {
  try { eps.value = await api<Ep[]>('/admin/endpoints') } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function create() {
  error.value = ''
  busy.value = true
  try {
    await api('/admin/endpoints', { json: form.value })
    form.value = { type: 'tenant', type_id: 0, endpoint_type: 'gateway', endpoint: '', priority: 0, bridge: false }
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function remove(ep: Ep) {
  const q = ep.type === 'global' ? '?confirm=true' : ''
  if (!confirm(`Delete endpoint ${ep.endpoint}?`)) return
  if (ep.type === 'global' && !confirmGlobal.value && !confirm('This is a GLOBAL upstream gateway. Really delete?')) return
  error.value = ''
  try { await api(`/admin/endpoints/${ep.id}${q}`, { method: 'DELETE' }); await load() }
  catch (e: any) { error.value = e.message }
}

async function editPriority(ep: Ep) {
  const p = prompt('Priority (lower = preferred; 666=no inbound)', String(ep.priority))
  if (p === null) return
  try {
    await api(`/admin/endpoints/${ep.id}`, { method: 'PUT', json: { ...ep, priority: Number(p) || 0 } })
    await load()
  } catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Endpoints <span class="muted">(live from gofaxserver)</span></h2>
      <form class="inline" @submit.prevent="create">
        <div>
          <label>Scope type</label>
          <select v-model="form.type"><option value="tenant">tenant</option><option value="number">number</option><option value="global">global</option></select>
        </div>
        <div><label>Type ID</label><input v-model.number="form.type_id" :disabled="form.type === 'global'" /></div>
        <div>
          <label>Endpoint kind</label>
          <select v-model="form.endpoint_type"><option>gateway</option><option>webhook</option><option>email</option></select>
        </div>
        <div><label>Value</label><input v-model="form.endpoint" placeholder="gw_name:IP | https://… | a@b.c" required /></div>
        <div><label>Priority</label><input v-model.number="form.priority" /></div>
        <div style="align-self:center"><label style="font-size:13px;color:var(--text)"><input type="checkbox" v-model="form.bridge" /> bridge</label></div>
        <button :disabled="busy">Add endpoint</button>
      </form>
      <p class="muted">Editing endpoints changes fax routing — prefer the CLI for carrier-level changes unless you know what you're doing.</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>ID</th><th>Scope</th><th>Type ID</th><th>Kind</th><th>Value</th><th>Pri</th><th>Bridge</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="ep in eps" :key="ep.id">
            <td>{{ ep.id }}</td>
            <td>{{ ep.type }}</td>
            <td>{{ ep.type_id }}</td>
            <td>{{ ep.endpoint_type }}</td>
            <td>{{ ep.endpoint }}</td>
            <td>{{ ep.priority }}</td>
            <td>{{ ep.bridge ? 'yes' : '' }}</td>
            <td class="actions-cell">
              <button class="secondary" @click="editPriority(ep)">Edit priority</button>
              <button class="danger" @click="remove(ep)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </main>
</template>
