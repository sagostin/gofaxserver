<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../../api'
import { useScopeTargets } from '../../scope'

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

interface Ep {
  id: number
  type: string
  type_id: number
  endpoint_type: string
  endpoint: string
  priority: number
  bridge: boolean
}

const templates = ref<Tpl[]>([])
const gateways = ref<GwStatus[]>([])
const unmanaged = ref<UnmanagedFile[]>([])
const endpoints = ref<Ep[]>([])
const error = ref('')
const busy = ref(false)
const fsBusy = ref('')
const fsMsg = ref('')
const editing = ref('') // gateway name when in update mode

// System-provided / structural variables get dedicated inputs or are handled
// by checkboxes; everything else renders as a generic text input.
const RESERVED = new Set(['name'])

const scope = useScopeTargets()

const form = ref({
  name: '',
  template_id: 0,
  type: 'tenant',
  scope_source: 'portal',
  type_id: 0,
  priority: 0,
  bridge: false,
  endpoint_ip: '',
})
const params = ref<Record<string, any>>({})

const scopeOptions = computed(() => scope.options(form.value.type, form.value.scope_source))

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

// --- provision-from-endpoint state ---
const feEndpoint = ref<Ep | null>(null)
const feTemplateId = ref(0)
const feParams = ref<Record<string, any>>({})

const feTemplate = computed(() => templates.value.find(t => t.id === feTemplateId.value))
const feVars = computed(() => (feTemplate.value?.variables || []).filter(v => !RESERVED.has(v)))

watch(feTemplate, (tpl) => {
  const next: Record<string, any> = {}
  for (const v of tpl?.variables || []) {
    if (RESERVED.has(v)) continue
    next[v] = v === 'register' ? false : (feParams.value[v] ?? '')
  }
  // realm defaults to the endpoint's IP part; the server does the same.
  const ip = feEndpoint.value?.endpoint.split(':')[1] || ''
  if (ip && !next.realm) next.realm = ip
  feParams.value = next
})

const managedEndpointIDs = computed(() => {
  const m = new Set<number>()
  for (const gs of gateways.value) {
    if (gs.gateway.endpoint_id) m.add(gs.gateway.endpoint_id)
  }
  return m
})

// Gateway-kind endpoints with no managing GatewayConfig — candidates for
// "provision from existing endpoint" (e.g. rows from an imported DB).
const unmanagedEndpoints = computed(() =>
  endpoints.value.filter(e => e.endpoint_type === 'gateway' && !managedEndpointIDs.value.has(e.id))
)

function parsedParams(gs: GwStatus): Record<string, any> {
  try { return JSON.parse(gs.gateway.params || '{}') } catch { return {} }
}

function isRegistered(gs: GwStatus): boolean {
  return parsedParams(gs).register === true
}

async function load() {
  error.value = ''
  try {
    const [t, g, epsList] = await Promise.all([
      api<Tpl[]>('/admin/gateway-templates'),
      api<{ gateways: GwStatus[]; unmanaged: UnmanagedFile[] }>('/admin/gateways'),
      api<Ep[]>('/admin/endpoints').catch(() => []),
      scope.load(),
    ])
    templates.value = t || []
    gateways.value = g.gateways || []
    unmanaged.value = g.unmanaged || []
    endpoints.value = epsList || []
    if (!form.value.template_id && templates.value.length) {
      form.value.template_id = templates.value[0].id
    }
    if (!feTemplateId.value && templates.value.length) {
      feTemplateId.value = templates.value[0].id
    }
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

function resetForm() {
  editing.value = ''
  form.value = { name: '', template_id: templates.value[0]?.id || 0, type: 'tenant', scope_source: 'portal', type_id: 0, priority: 0, bridge: false, endpoint_ip: '' }
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
    if (form.value.type !== 'global') body.scope_source = form.value.scope_source
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
    scope_source: 'direct',
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

// --- FreeSWITCH profile control ---

async function rescanProfile() {
  error.value = ''
  fsMsg.value = ''
  fsBusy.value = 'rescan'
  try {
    await api('/admin/freeswitch/rescan', { method: 'POST' })
    fsMsg.value = 'Profile rescanned (reloadxml + sofia rescan).'
    await load()
  } catch (e: any) { error.value = e.message } finally { fsBusy.value = '' }
}

async function restartProfile() {
  if (!confirm('Restart the sofia fax profile? This DROPS all active calls on the profile.')) return
  if (!confirm('Really restart? In-progress faxes on this profile will fail.')) return
  error.value = ''
  fsMsg.value = ''
  fsBusy.value = 'restart'
  try {
    await api('/admin/freeswitch/profile/restart?confirm=true', { method: 'POST' })
    fsMsg.value = 'Profile restarted.'
    await load()
  } catch (e: any) { error.value = e.message } finally { fsBusy.value = '' }
}

// --- provision from existing endpoint ---

function startFromEndpoint(ep: Ep) {
  feEndpoint.value = ep
  if (!feTemplateId.value && templates.value.length) feTemplateId.value = templates.value[0].id
}

async function provisionFromEndpoint() {
  if (!feEndpoint.value) return
  error.value = ''
  busy.value = true
  try {
    const p: Record<string, any> = {}
    for (const [k, v] of Object.entries(feParams.value)) {
      if (v === '' || v === false) continue
      p[k] = v
    }
    await api('/admin/gateways/from-endpoint', {
      json: { endpoint_id: feEndpoint.value.id, template_id: feTemplateId.value, params: p },
    })
    feEndpoint.value = null
    feParams.value = {}
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>
        FreeSWITCH profile
        <span class="muted">(sofia profile hosting the gateways)</span>
      </h2>
      <div>
        <button class="secondary" :disabled="!!fsBusy" @click="rescanProfile">
          {{ fsBusy === 'rescan' ? 'Rescanning…' : 'Rescan profile' }}
        </button>
        <button class="danger" style="margin-left:8px" :disabled="!!fsBusy" @click="restartProfile">
          {{ fsBusy === 'restart' ? 'Restarting…' : 'Restart profile' }}
        </button>
      </div>
      <p class="muted">
        Rescan is safe (reloadxml + rescan, no call impact). Restart drops all active calls on the profile.
      </p>
      <p v-if="fsMsg" class="muted">{{ fsMsg }}</p>
    </div>

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
        <div v-if="form.type !== 'global'">
          <label>Scope source</label>
          <select v-model="form.scope_source" :disabled="!!editing" @change="form.type_id = 0">
            <option value="portal">portal (orgs/numbers)</option>
            <option value="direct">direct (gofaxserver tenant)</option>
          </select>
        </div>
        <div v-if="form.type !== 'global'">
          <label>Target</label>
          <select v-model.number="form.type_id" :disabled="!!editing" required>
            <option :value="0" disabled>select…</option>
            <option v-for="o in scopeOptions" :key="o.id" :value="o.id">{{ o.label }}</option>
          </select>
        </div>
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

    <div v-if="unmanagedEndpoints.length" class="panel">
      <h3>Unmanaged gateway endpoints <span class="muted">(routing rows without a managed FreeSWITCH gateway)</span></h3>
      <table>
        <thead>
          <tr><th>ID</th><th>Scope</th><th>Type ID</th><th>Value</th><th>Pri</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="ep in unmanagedEndpoints" :key="ep.id">
            <td>{{ ep.id }}</td>
            <td>{{ ep.type }}</td>
            <td>{{ ep.type_id }}</td>
            <td>{{ ep.endpoint }}</td>
            <td>{{ ep.priority }}</td>
            <td class="actions-cell">
              <button class="secondary" @click="startFromEndpoint(ep)">Provision…</button>
            </td>
          </tr>
        </tbody>
      </table>

      <div v-if="feEndpoint" style="margin-top:12px">
        <h4>Provision gateway for endpoint #{{ feEndpoint.id }} ({{ feEndpoint.endpoint }})</h4>
        <form class="inline" @submit.prevent="provisionFromEndpoint">
          <div>
            <label>Template</label>
            <select v-model.number="feTemplateId">
              <option v-for="t in templates" :key="t.id" :value="t.id">{{ t.name }} — {{ t.description }}</option>
            </select>
          </div>
          <div v-for="v in feVars" :key="v">
            <template v-if="v === 'register'">
              <label style="font-size:13px;color:var(--text);align-self:center"><input type="checkbox" v-model="feParams.register" /> register (SIP auth)</label>
            </template>
            <template v-else>
              <label>{{ v }}</label>
              <input v-model="feParams[v]" :type="v === 'password' ? 'password' : 'text'" :placeholder="v === 'realm' ? 'defaults to endpoint IP' : ''" />
            </template>
          </div>
        </form>
        <div style="margin-top:12px">
          <button :disabled="busy" @click="provisionFromEndpoint">Provision from endpoint</button>
          <button class="secondary" style="margin-left:8px" @click="feEndpoint = null">Cancel</button>
        </div>
      </div>
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
