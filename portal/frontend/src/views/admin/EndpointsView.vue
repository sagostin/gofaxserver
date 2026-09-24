<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../../api'
import { useScopeTargets } from '../../scope'

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
const managedBy = ref<Record<number, string>>({}) // endpoint_id -> gateway name
const error = ref('')
const busy = ref(false)
const form = ref({ type: 'tenant', scope_source: 'portal', type_id: 0, endpoint_type: 'gateway', endpoint: '', priority: 0, bridge: false })
const confirmGlobal = ref(false)

const scope = useScopeTargets()
const scopeOptions = computed(() => scope.options(form.value.type, form.value.scope_source))

async function load() {
  try {
    const [list, gw] = await Promise.all([
      api<Ep[]>('/admin/endpoints'),
      api<{ gateways: { gateway: { name: string; endpoint_id: number } }[] }>('/admin/gateways').catch(() => null),
      scope.load(),
    ])
    eps.value = list
    const map: Record<number, string> = {}
    for (const gs of gw?.gateways || []) {
      if (gs.gateway.endpoint_id) map[gs.gateway.endpoint_id] = gs.gateway.name
    }
    managedBy.value = map
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function create() {
  error.value = ''
  busy.value = true
  try {
    const body: Record<string, any> = { ...form.value }
    if (form.value.type === 'global') { body.type_id = 0; delete body.scope_source }
    await api('/admin/endpoints', { json: body })
    form.value = { type: 'tenant', scope_source: 'portal', type_id: 0, endpoint_type: 'gateway', endpoint: '', priority: 0, bridge: false }
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
        <div v-if="form.type !== 'global'">
          <label>Scope source</label>
          <select v-model="form.scope_source" @change="form.type_id = 0">
            <option value="portal">portal (orgs/numbers)</option>
            <option value="direct">direct (gofaxserver tenant)</option>
          </select>
        </div>
        <div v-if="form.type !== 'global'">
          <label>Target</label>
          <select v-model.number="form.type_id" required>
            <option :value="0" disabled>select…</option>
            <option v-for="o in scopeOptions" :key="o.id" :value="o.id">{{ o.label }}</option>
          </select>
        </div>
        <div>
          <label>Endpoint kind</label>
          <select v-model="form.endpoint_type"><option>gateway</option><option>webhook</option><option>email</option><option>portal</option></select>
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
            <td>
              {{ ep.endpoint }}
              <span v-if="managedBy[ep.id]" class="muted" :title="`Managed by gateway ${managedBy[ep.id]} — edit via the Gateways tab`">⚙ {{ managedBy[ep.id] }}</span>
            </td>
            <td>{{ ep.priority }}</td>
            <td>{{ ep.bridge ? 'yes' : '' }}</td>
            <td class="actions-cell">
              <template v-if="!managedBy[ep.id]">
                <button class="secondary" @click="editPriority(ep)">Edit priority</button>
                <button class="danger" @click="remove(ep)">Delete</button>
              </template>
              <span v-else class="muted">via Gateways</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </main>
</template>
