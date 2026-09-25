<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'
import Modal from '../../components/Modal.vue'
import NotifyRulesEditor from '../../components/NotifyRulesEditor.vue'
import ActionsMenu, { type ActionItem } from '../../components/ActionsMenu.vue'

interface Org {
  id: number
  name: string
  gofax_tenant_id: number
  svc_username: string
  tenant_notify: string
  totp_required: boolean
  active: boolean
  user_count: number
  number_count: number
}

const orgs = ref<Org[]>([])
const name = ref('')
const error = ref('')
const busy = ref(false)
const report = ref<any>(null)

async function load() {
  try { orgs.value = await api<Org[]>('/admin/orgs') } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function create() {
  error.value = ''
  busy.value = true
  try {
    await api('/admin/orgs', { json: { name: name.value } })
    name.value = ''
    await load()
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function toggleActive(o: Org) {
  error.value = ''
  try { await api(`/admin/orgs/${o.id}`, { method: 'PUT', json: { active: !o.active } }); await load() }
  catch (e: any) { error.value = e.message }
}

async function toggleTOTP(o: Org) {
  if (!o.totp_required && !confirm(`Require 2FA for ${o.name}? Every portal user in this org will be forced to set up an authenticator app at their next login.`)) return
  error.value = ''
  try { await api(`/admin/orgs/${o.id}`, { method: 'PUT', json: { totp_required: !o.totp_required } }); await load() }
  catch (e: any) { error.value = e.message }
}

async function purge(o: Org) {
  if (!confirm(`Purge ${o.name}? Deletes the upstream tenant, numbers and deactivates its portal users. This cannot be undone.`)) return
  error.value = ''
  try {
    await api(`/admin/orgs/${o.id}?purge=true`, { method: 'DELETE' })
    await load()
  } catch (e: any) { error.value = e.message }
}

async function reconcile(o: Org) {
  error.value = ''
  report.value = null
  try { report.value = await api(`/admin/orgs/${o.id}/reconcile`) }
  catch (e: any) { error.value = e.message }
}

async function rotateCredentials(o: Org) {
  if (!confirm(`Rotate the gofaxserver service-account credentials for ${o.name}? A fresh password + API key are generated, pushed upstream, verified, then stored. Brief send failures are possible while in flight.`)) return
  error.value = ''
  try {
    const r = await api<any>(`/admin/orgs/${o.id}/credentials/rotate`, { method: 'POST' })
    alert(r.recreated_upstream_user
      ? 'Service account was missing upstream — recreated with fresh credentials.'
      : 'Service account credentials rotated and verified.')
  } catch (e: any) { error.value = e.message }
}

async function resyncNotify(o: Org) {
  error.value = ''
  report.value = null
  try { report.value = await api<any>(`/admin/orgs/${o.id}/notify/resync`, { method: 'POST' }) }
  catch (e: any) { error.value = e.message }
}

async function editTenantNotify(o: Org) {
  tnFor.value = o
  tnValue.value = o.tenant_notify
}

const tnFor = ref<Org | null>(null)
const tnValue = ref('')

async function saveTenantNotify() {
  if (!tnFor.value) return
  error.value = ''
  try {
    await api(`/admin/orgs/${tnFor.value.id}`, { method: 'PUT', json: { tenant_notify: tnValue.value } })
    tnFor.value = null
    await load()
  } catch (e: any) { error.value = e.message }
}

function orgActions(o: Org): ActionItem[] {
  return [
    { label: o.active ? 'Deactivate' : 'Reactivate', danger: o.active, onClick: () => toggleActive(o) },
    { label: o.totp_required ? 'Disable 2FA requirement' : 'Require 2FA', onClick: () => toggleTOTP(o) },
    { label: 'Reconcile', hint: 'Compare portal state against gofaxserver and report drift', onClick: () => reconcile(o) },
    { label: 'Re-sync notify', hint: 'Re-derive and push all number notify rules for this org', onClick: () => resyncNotify(o) },
    { label: 'Tenant notify', hint: 'Org-level notify rules pushed to the gofaxserver tenant', onClick: () => editTenantNotify(o) },
    { label: 'Rotate credentials', hint: 'Fresh password + API key for the service account, pushed upstream and verified', onClick: () => rotateCredentials(o) },
    { label: 'Purge', danger: true, hint: 'Delete the upstream tenant, numbers and deactivate portal users', onClick: () => purge(o) },
  ]
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Organizations</h2>
      <form class="inline" @submit.prevent="create">
        <div><label>New organization name</label><input v-model="name" required /></div>
        <button :disabled="busy">Create org</button>
        <span class="muted">Provisions a gofaxserver tenant + service account.</span>
      </form>
      <p v-if="error" class="error">{{ error }}</p>
    </div>

    <div class="panel" v-if="report">
      <h3>Report</h3>
      <pre style="background:#f3f4f6;padding:10px;border-radius:8px;overflow:auto">{{ JSON.stringify(report, null, 2) }}</pre>
      <button class="secondary" @click="report = null">Close</button>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>ID</th><th>Name</th><th>Tenant</th><th>Service acct</th><th>Users</th><th>Numbers</th><th>2FA</th><th>Status</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="o in orgs" :key="o.id">
            <td>{{ o.id }}</td>
            <td>{{ o.name }}</td>
            <td>#{{ o.gofax_tenant_id }}</td>
            <td class="muted">{{ o.svc_username }}
              <div v-if="o.tenant_notify" class="muted" style="font-size:11px" :title="o.tenant_notify">tenant notify managed</div>
            </td>
            <td>{{ o.user_count }}</td>
            <td>{{ o.number_count }}</td>
            <td><span class="badge" :class="o.totp_required ? 'active' : 'inactive'">{{ o.totp_required ? 'required' : 'off' }}</span></td>
            <td><span class="badge" :class="o.active ? 'active' : 'inactive'">{{ o.active ? 'active' : 'inactive' }}</span></td>
            <td class="actions-cell">
              <RouterLink :to="`/admin/orgs/${o.id}/users`"><button>Manage</button></RouterLink>
              <ActionsMenu :items="orgActions(o)" />
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Modal v-if="tnFor" :title="`Tenant notify — ${tnFor.name}`" @close="tnFor = null">
      <p class="muted" style="margin-top:0">
        These rules apply to <strong>all</strong> of this org's numbers and are pushed to the gofaxserver tenant.
        Saving with no rules clears the upstream tenant notify and hands control back to out-of-band edits.
      </p>
      <NotifyRulesEditor v-model="tnValue" />
      <template #footer>
        <button class="secondary" @click="tnFor = null">Cancel</button>
        <button @click="saveTenantNotify">Save &amp; push upstream</button>
      </template>
    </Modal>
  </main>
</template>
