<script setup lang="ts">
import { onMounted, ref } from 'vue'
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

const tenants = ref<Tenant[]>([])
const orgTenantIDs = ref<Set<number>>(new Set()) // gofax tenant IDs claimed by portal orgs
const error = ref('')
const busy = ref(false)

function orgManaged(tid: number) { return orgTenantIDs.value.has(tid) }

// Create tenant form
const tForm = ref({ name: '', notify: '' })
// Edit tenant state
const tEdit = ref<Tenant | null>(null)
const tEditForm = ref({ name: '', notify: '' })

async function load() {
  error.value = ''
  try {
    tenants.value = (await api<Tenant[]>('/admin/tenants')) || []
    const orgs = (await api<{ gofax_tenant_id: number }[]>('/admin/orgs')) || []
    orgTenantIDs.value = new Set(orgs.map((o) => o.gofax_tenant_id))
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

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
}async function removeTenant(t: Tenant) {
  if (!confirm(`Delete tenant "${t.name}" (#${t.id})? Its numbers and users are NOT automatically removed upstream.`)) return
  error.value = ''
  try {
    await api(`/admin/tenants/${t.id}?confirm=true`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}

function tenantActions(t: Tenant): ActionItem[] {
  return [
    { label: 'Edit', onClick: () => startEditTenant(t) },
    { label: 'Delete', danger: true, onClick: () => removeTenant(t) },
  ]
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
          <tr v-for="t in tenants" :key="t.id">
            <td>{{ t.id }}</td>
            <td>
              {{ t.name }}
              <span v-if="orgManaged(t.id)" class="badge active" title="Claimed by a portal organization">portal org</span>
            </td>
            <td>{{ t.notify || '—' }}</td>
            <td>{{ t.numbers?.length || 0 }}</td>
            <td class="actions-cell">
              <RouterLink :to="`/admin/tenants/${t.id}`"><button>Manage</button></RouterLink>
              <ActionsMenu :items="tenantActions(t)" />
            </td>
          </tr>
          <tr v-if="!tenants.length"><td colspan="5" class="muted">No tenants on gofaxserver yet.</td></tr>
        </tbody>
      </table>
    </div>

    <Modal v-if="tEdit" :title="`Edit tenant #${tEdit.id}`" @close="tEdit = null">
      <div class="field">
        <label>Name</label>
        <input v-model="tEditForm.name" required />
      </div>
      <div class="field">
        <label>Notify rules</label>
        <NotifyRulesEditor v-model="tEditForm.notify" />
        <p v-if="orgManaged(tEdit.id)" class="muted" style="margin:6px 0 0">
          Portal-managed tenant: org-level rules are set via Organizations → Tenant notify. Edits here may be overwritten by the portal.
        </p>
      </div>
      <template #footer>
        <button class="secondary" @click="tEdit = null">Cancel</button>
        <button :disabled="!tEditForm.name" @click="saveTenant">Save</button>
      </template>
    </Modal>
  </main>
</template>
