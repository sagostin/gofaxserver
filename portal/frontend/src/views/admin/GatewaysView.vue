<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../../api'

interface Tpl {
  id: number
  name: string
  description: string
  variables: string[]
}

interface GwStatus {
  gateway: {
    id: number
    name: string
    template_id: number
    template: Tpl
    params: string
    endpoint_id: number
    last_state: string
    created_at: string
  }
  file: string
  state: string
  exists_on_disk: boolean
}

interface UnmanagedFile {
  file: string
  name: string
}

const templates = ref<Tpl[]>([])
const gateways = ref<GwStatus[]>([])
const unmanaged = ref<UnmanagedFile[]>([])
const error = ref('')
const busy = ref(false)
const editing = ref('') // gateway name when in update mode

// System-provided / structural variables get dedicated inputs or are handled
// by checkboxes; everything else renders as a generic text input.
const RESERVED = new Set(['name'])

const form = ref({
  name: '',
  template_id: 0,
  type: 'tenant',
  type_id: 0,
  priority: 0,
  bridge: false,
  endpoint_ip: '',
})
const params = ref<Record<string, any>>({})

const selectedTemplate = computed(() => templates.value.find(t => t.id === form.value.template_id))
const dynamicVars = computed(() => (selectedTemplate.value?.variables || []).filter(v => !RESERVED.has(v)))

watch(selectedTemplate, (tpl) => {
  // Reset params when switching templates, keeping declared vars present.
  const next: Record<string, any> = {}
  for (const v of tpl?.variables || []) {
    if (RESERVED.has(v)) continue
    next[v] = v === 'register' ? false : (params.value[v] ?? '')
  }
  params.value = next
})

function parsedParams(gs: GwStatus): Record<string, any> {
  try { return JSON.parse(gs.gateway.params || '{}') } catch { return {} }
}

function isRegistered(gs: GwStatus): boolean {
  return parsedParams(gs).register === true
}

async function load() {
  error.value = ''
  try {
    const [t, g] = await Promise.all([
      api<Tpl[]>('/admin/gateway-templates'),
      api<{ gateways: GwStatus[]; unmanaged: UnmanagedFile[] }>('/admin/gateways'),
    ])
    templates.value = t || []
    gateways.value = g.gateways || []
    unmanaged.value = g.unmanaged || []
    if (!form.value.template_id && templates.value.length) {
      form.value.template_id = templates.value[0].id
    }
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

function resetForm() {
  editing.value = ''
  form.value = { name: '', template_id: templates.value[0]?.id || 0, type: 'tenant', type_id: 0, priority: 0, bridge: false, endpoint_ip: '' }
}

function payload() {
  const p: Record<string, any> = {}
  for (const [k, v] of Object.entries(params.value)) {
    if (v === '' || v === false) continue // let the server apply defaults
    p[k] = v
  }
  const body: Record<string, any> = {
    name: form.value.name,
    template_id: form.value.template_id,
    params: p,
    endpoint_ip: form.value.endpoint_ip,
  }
  // Scope/priority/bridge are only meaningful on provision; on update the
  // server preserves them from the existing endpoint row.
  if (!editing.value) {
    body.type = form.value.type
    body.type_id = form.value.type === 'global' ? 0 : form.value.type_id
    body.priority = form.value.priority
    body.bridge = form.value.bridge
  }
  return body
}

async function submit() {
  error.value = ''
  busy.value = true
  try {
    if (editing.value) {
      await api(`/admin/gateways/${editing.value}`, { method: 'PUT', json: payload() })
    } else {
      await api('/admin/gateways', { json: payload() })
    }
    resetForm()
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

function edit(gs: GwStatus) {
  editing.value = gs.gateway.name
  form.value = {
    name: gs.gateway.name,
    template_id: gs.gateway.template_id,
    type: 'tenant', // scope is stored on the endpoint; adjust via the Endpoints tab
    type_id: 0,
    priority: 0,
    bridge: false,
    endpoint_ip: '',
  }
  params.value = parsedParams(gs)
}

async function remove(gs: GwStatus) {
  if (!confirm(`Deprovision gateway ${gs.gateway.name}? This removes the FreeSWITCH XML, tears the gateway down, and deletes the linked endpoint.`)) return
  error.value = ''
  try {
    await api(`/admin/gateways/${gs.gateway.name}?confirm=true`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function repair(gs: GwStatus) {
  error.value = ''
  try {
    await api(`/admin/gateways/${gs.gateway.name}/repair`, { method: 'POST' })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function adopt(u: UnmanagedFile) {
  if (!confirm(`Adopt ${u.file} into managed gateways${u.name ? ` as "${u.name}"` : ''}?`)) return
  error.value = ''
  try {
    await api('/admin/gateways/adopt', { json: { file: u.file } })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function removeUnmanaged(u: UnmanagedFile) {
  if (!u.name) { error.value = 'Cannot delete: no gateway name could be parsed from this file'; return }
  if (!confirm(`Delete unmanaged gateway file ${u.file} and tear down "${u.name}" in FreeSWITCH?`)) return
  error.value = ''
  try {
    await api(`/admin/gateways/unmanaged/${u.name}?confirm=true`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>{{ editing ? `Update gateway ${editing}` : 'Provision FreeSWITCH gateway' }}</h2>
      <p class="muted">
        Renders the selected template into the FreeSWITCH gateways directory, reloads the
        sofia profile, and creates the linked endpoint — one step.
        The gateway name must match <code>name=</code> in the XML and the endpoint prefix.
      </p>
      <form class="inline" @submit.prevent="submit">
        <div><label>Name</label><input v-model="form.name" placeholder="pbx_acme" pattern="[a-z0-9_]+" :disabled="!!editing" required /></div>
        <div>
          <label>Template</label>
          <select v-model.number="form.template_id">
            <option v-for="t in templates" :key="t.id" :value="t.id">{{ t.name }} — {{ t.description }}</option>
          </select>
        </div>
        <div>
          <label>Scope</label>
          <select v-model="form.type" :disabled="!!editing"><option value="tenant">tenant</option><option value="number">number</option><option value="global">global</option></select>
        </div>
        <div><label>Type ID</label><input v-model.number="form.type_id" :disabled="form.type === 'global' || !!editing" /></div>
        <div><label>Priority</label><input v-model.number="form.priority" /></div>
        <div style="align-self:center"><label style="font-size:13px;color:var(--text)"><input type="checkbox" v-model="form.bridge" /> bridge</label></div>
        <div><label>Endpoint IP <span class="muted">(ACL)</span></label><input v-model="form.endpoint_ip" placeholder="defaults to realm" /></div>
      </form>

      <form v-if="dynamicVars.length" class="inline" @submit.prevent="submit">
        <div v-for="v in dynamicVars" :key="v">
          <template v-if="v === 'register'">
            <label style="font-size:13px;color:var(--text);align-self:center"><input type="checkbox" v-model="params.register" /> register (SIP auth)</label>
          </template>
          <template v-else>
            <label>{{ v }}</label>
            <input v-model="params[v]" :type="v === 'password' ? 'password' : 'text'" :required="v === 'realm'" :placeholder="v === 'extension' ? 'auto_to_user' : ''" />
          </template>
        </div>
      </form>

      <div style="margin-top:12px">
        <button :disabled="busy" @click="submit">{{ editing ? 'Update gateway' : 'Provision gateway' }}</button>
        <button v-if="editing" class="secondary" style="margin-left:8px" @click="resetForm">Cancel</button>
      </div>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel">
      <h3>Provisioned gateways <span class="muted">(live from gofaxserver + FreeSWITCH)</span></h3>
      <table>
        <thead>
          <tr><th>Name</th><th>Template</th><th>Realm</th><th>FS state</th><th>XML on disk</th><th>Endpoint</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="gs in gateways" :key="gs.gateway.id">
            <td>{{ gs.gateway.name }}</td>
            <td>{{ gs.gateway.template?.name || (gs.gateway.template_id === 0 ? 'adopted' : gs.gateway.template_id) }}</td>
            <td>{{ parsedParams(gs).realm || '' }}</td>
            <td>
              {{ gs.state }}
              <span v-if="isRegistered(gs) && gs.gateway.last_state" class="muted">(monitored: {{ gs.gateway.last_state }})</span>
            </td>
            <td>
              {{ gs.exists_on_disk ? 'yes' : 'missing' }}
              <button v-if="!gs.exists_on_disk" class="secondary" style="margin-left:6px" @click="repair(gs)">Re-render</button>
            </td>
            <td>{{ gs.gateway.endpoint_id ? '#' + gs.gateway.endpoint_id : '—' }}</td>
            <td class="actions-cell">
              <button class="secondary" @click="edit(gs)">Edit</button>
              <button class="danger" @click="remove(gs)">Deprovision</button>
            </td>
          </tr>
          <tr v-if="!gateways.length"><td colspan="7" class="muted">No gateways provisioned yet.</td></tr>
        </tbody>
      </table>
    </div>

    <div v-if="unmanaged.length" class="panel">
      <h3>Unmanaged files <span class="muted">(XML in the gateways dir with no DB record)</span></h3>
      <table>
        <thead>
          <tr><th>File</th><th>Gateway name</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="u in unmanaged" :key="u.file">
            <td>{{ u.file }}</td>
            <td>{{ u.name || 'unparseable' }}</td>
            <td class="actions-cell">
              <button class="secondary" :disabled="!u.name" @click="adopt(u)">Adopt</button>
              <button class="danger" :disabled="!u.name" @click="removeUnmanaged(u)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </main>
</template>
