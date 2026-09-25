<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'
import FaxLegList, { type FaxLeg } from '../../components/FaxLegList.vue'

interface Org { id: number; name: string; gofax_tenant_id: number }
interface Tenant { id: number; name: string }
interface Job {
  id: number
  job_uuid: string
  username: string
  org_name: string
  caller_number: string
  callee_number: string
  original_filename: string
  status: string
  pages: number
  attempts: number
  result_text: string
  last_error: string
  submitted_at: string
}
interface ResultGroup {
  job_uuid: string
  caller_id_number: string
  caller_id_name: string
  callee_number: string
  src_tenant_id: number
  dst_tenant_id: number
  src_tenant_name: string
  dst_tenant_name: string
  attempts: number
  leg_types: string[]
  success: boolean
  status: string
  transferred_pages: number
  total_pages: number
  first_ts: string
  last_ts: string
  legs: FaxLeg[]
}
interface ResultList { total: number; items: ResultGroup[] }

const view = ref<'portal' | 'all'>('portal')
const error = ref('')

// --- portal jobs state ---
const jobs = ref<Job[]>([])
const orgs = ref<Org[]>([])
const orgFilter = ref(0)
const statusFilter = ref('')

// --- all fax results state (grouped by job UUID) ---
const groups = ref<ResultGroup[]>([])
const total = ref(0)
const page = ref(0)
const pageSize = 50
const tenants = ref<Tenant[]>([])
const tenantFilter = ref(0)
const typeFilter = ref('')
const successFilter = ref('')
const numberSearch = ref('')
const uuidSearch = ref('')
const fromDate = ref('')
const toDate = ref('')

// Portal-jobs expand fetches attempts; all-results groups carry legs inline.
const expanded = ref<Record<string, FaxLeg[]>>({})
const expandedGroups = ref<Record<string, boolean>>({})
const copied = ref('')

async function loadOrgsTenants() {
  try { orgs.value = await api<Org[]>('/admin/orgs') } catch { /* non-fatal */ }
  try { tenants.value = await api<Tenant[]>('/admin/upstream/tenants') } catch { /* non-fatal */ }
}

async function loadPortalJobs() {
  try {
    const qs = new URLSearchParams()
    if (orgFilter.value) qs.set('org_id', String(orgFilter.value))
    if (statusFilter.value) qs.set('status', statusFilter.value)
    jobs.value = await api<Job[]>('/admin/jobs?' + qs.toString())
    error.value = ''
  } catch (e: any) { error.value = e.message }
}

async function loadAllResults() {
  try {
    const qs = new URLSearchParams({ limit: String(pageSize), offset: String(page.value * pageSize) })
    if (tenantFilter.value) qs.set('tenant_id', String(tenantFilter.value))
    if (typeFilter.value) qs.set('result_type', typeFilter.value)
    if (successFilter.value) qs.set('success', successFilter.value)
    if (numberSearch.value.trim()) qs.set('number', numberSearch.value.trim())
    if (uuidSearch.value.trim()) qs.set('job_uuid', uuidSearch.value.trim())
    if (fromDate.value) qs.set('from', fromDate.value)
    if (toDate.value) qs.set('to', toDate.value)
    const d = await api<ResultList>('/admin/fax-results?' + qs.toString())
    groups.value = d.items || []
    total.value = d.total || 0
    expandedGroups.value = {}
    error.value = ''
  } catch (e: any) { error.value = e.message }
}

function load() { view.value === 'portal' ? loadPortalJobs() : loadAllResults() }

// A job whose only persisted legs are submissions is queued/in-flight — no
// real attempt has concluded, so it must not render as failed. The group
// status carries the intake lifecycle state ("queued" / "processed").
function isIntakeGroup(g: ResultGroup): boolean {
  return g.attempts === 0 && g.leg_types.length > 0 &&
    g.leg_types.every(t => t === 'submission')
}
function outcomeClass(g: ResultGroup): string {
  return g.success ? 'success' : isIntakeGroup(g) ? 'queued' : 'failed'
}
function outcomeLabel(g: ResultGroup): string {
  if (g.success) return 'success'
  if (isIntakeGroup(g)) return g.status || 'queued'
  return 'failed'
}
function switchView(v: 'portal' | 'all') {
  view.value = v
  expanded.value = {}
  expandedGroups.value = {}
  load()
}
onMounted(() => { loadOrgsTenants(); load() })

function searchAll() { page.value = 0; loadAllResults() }
function nextPage() { page.value++; loadAllResults() }
function prevPage() { if (page.value > 0) { page.value--; loadAllResults() } }

// Expand a portal job to show its upstream attempts (persisted result legs).
async function toggleAttempts(j: Job) {
  if (j.job_uuid in expanded.value) {
    delete expanded.value[j.job_uuid]
    return
  }
  try {
    const d = await api<ResultList>('/admin/fax-results?job_uuid=' + encodeURIComponent(j.job_uuid))
    expanded.value[j.job_uuid] = d.items?.[0]?.legs || []
  } catch (e: any) { error.value = e.message }
}

function toggleGroup(uuid: string) {
  expandedGroups.value[uuid] = !expandedGroups.value[uuid]
}

function tenantLabel(id: number): string {
  if (!id) return '—'
  const org = orgs.value.find((o) => o.gofax_tenant_id === id)
  if (org) return org.name
  const t = tenants.value.find((t) => t.id === id)
  return t ? `${t.name} (#${id})` : `#${id}`
}

function fmt(ts: string | null): string {
  return ts ? new Date(ts).toLocaleString() : '—'
}

function span(first: string, last: string): string {
  const ms = new Date(last).getTime() - new Date(first).getTime()
  if (isNaN(ms) || ms <= 0) return ''
  const s = ms / 1000
  if (s < 1) return `${ms} ms`
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)} s`
  const m = Math.floor(s / 60)
  return `${m} m ${Math.round(s % 60)} s`
}

function groupPages(g: ResultGroup): string {
  if (!g.transferred_pages && !g.total_pages) return '—'
  return `${g.transferred_pages}/${g.total_pages || '—'}`
}

async function copyUuid(u: string) {
  if (!u) return
  try {
    await navigator.clipboard.writeText(u)
    copied.value = u
    setTimeout(() => { if (copied.value === u) copied.value = '' }, 1500)
  } catch { /* clipboard unavailable */ }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Fax jobs</h2>
      <div class="inline" style="margin-bottom:10px">
        <button :class="view === 'portal' ? '' : 'secondary'" @click="switchView('portal')">Portal jobs</button>
        <button :class="view === 'all' ? '' : 'secondary'" @click="switchView('all')">All fax results</button>
      </div>

      <!-- ===== Portal-submitted jobs ===== -->
      <template v-if="view === 'portal'">
        <form class="inline" @submit.prevent="loadPortalJobs">
          <div>
            <label>Org</label>
            <select v-model="orgFilter"><option :value="0">All</option><option v-for="o in orgs" :key="o.id" :value="o.id">{{ o.name }}</option></select>
          </div>
          <div>
            <label>Status</label>
            <select v-model="statusFilter">
              <option value="">All</option><option>queued</option><option>sending</option><option>success</option><option>failed</option>
            </select>
          </div>
          <button class="secondary">Apply</button>
        </form>

        <table style="margin-top:10px">
          <thead>
            <tr><th>Submitted</th><th>User / Org</th><th>Doc</th><th>From → To</th><th>Status</th><th>Pages</th><th>Att.</th><th>Result</th><th></th></tr>
          </thead>
          <tbody>
            <template v-for="j in jobs" :key="j.id">
              <tr>
                <td>{{ fmt(j.submitted_at) }}</td>
                <td>{{ j.username }} / {{ j.org_name }}</td>
                <td>{{ j.original_filename }}</td>
                <td>{{ j.caller_number }} → {{ j.callee_number }}</td>
                <td><span class="badge" :class="j.status">{{ j.status }}</span></td>
                <td>{{ j.pages || '—' }}</td>
                <td>{{ j.attempts }}</td>
                <td>{{ j.result_text || j.last_error || '—' }}</td>
                <td><button class="secondary" @click="toggleAttempts(j)">{{ j.job_uuid in expanded ? 'Hide' : 'Attempts' }}</button></td>
              </tr>
              <tr v-if="j.job_uuid in expanded">
                <td colspan="9">
                  <FaxLegList v-if="expanded[j.job_uuid].length" :legs="expanded[j.job_uuid]" />
                  <p v-else class="muted">No upstream attempts recorded yet.</p>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </template>

      <!-- ===== All upstream fax results, grouped by job ===== -->
      <template v-else>
        <form class="inline" @submit.prevent="searchAll">
          <div>
            <label>Tenant</label>
            <select v-model="tenantFilter">
              <option :value="0">All</option>
              <option v-for="t in tenants" :key="t.id" :value="t.id">{{ t.name }} (#{{ t.id }})</option>
            </select>
          </div>
          <div>
            <label>Type</label>
            <select v-model="typeFilter">
              <option value="">All</option>
              <option value="transmission">transmission</option>
              <option value="reception">reception</option>
              <option value="bridge">bridge</option>
              <option value="delivery">delivery</option>
              <option value="submission">submission</option>
            </select>
          </div>
          <div>
            <label>Success</label>
            <select v-model="successFilter">
              <option value="">All</option>
              <option value="true">success</option>
              <option value="false">failed</option>
            </select>
          </div>
          <div>
            <label>Number</label>
            <input v-model="numberSearch" placeholder="caller or callee" />
          </div>
          <div>
            <label>Job UUID</label>
            <input v-model="uuidSearch" placeholder="exact uuid" style="width:220px" />
          </div>
          <div>
            <label>From</label>
            <input v-model="fromDate" type="date" />
          </div>
          <div>
            <label>To</label>
            <input v-model="toDate" type="date" />
          </div>
          <button class="secondary" type="submit">Apply</button>
        </form>

        <table style="margin-top:10px">
          <thead>
            <tr><th>Last activity</th><th>Type</th><th>From → To</th><th>Tenant (src → dst)</th><th>Outcome</th><th>Att.</th><th>Pages</th><th></th></tr>
          </thead>
          <tbody>
            <template v-for="g in groups" :key="g.job_uuid">
              <tr>
                <td>{{ fmt(g.last_ts) }}</td>
                <td>
                  <span v-for="t in g.leg_types" :key="t" class="badge" :class="t" style="margin-right:4px">{{ t }}</span>
                </td>
                <td>{{ g.caller_id_number || '—' }} → {{ g.callee_number || '—' }}</td>
                <td>{{ tenantLabel(g.src_tenant_id) }} → {{ tenantLabel(g.dst_tenant_id) }}</td>
                <td><span class="badge" :class="outcomeClass(g)">{{ outcomeLabel(g) }}</span></td>
                <td>{{ g.attempts }}</td>
                <td>{{ groupPages(g) }}</td>
                <td><button class="secondary" @click="toggleGroup(g.job_uuid)">{{ expandedGroups[g.job_uuid] ? 'Hide' : 'Legs' }}</button></td>
              </tr>
              <tr v-if="expandedGroups[g.job_uuid]">
                <td colspan="8">
                  <p class="muted" style="font-size:12px; margin:4px 0 6px">
                    <span class="copyable" :title="copied === g.job_uuid ? 'Copied!' : 'Click to copy'" @click="copyUuid(g.job_uuid)">
                      {{ copied === g.job_uuid ? '✓ copied' : 'Job ' + g.job_uuid }}
                    </span>
                    · {{ g.attempts }} attempt{{ g.attempts === 1 ? '' : 's' }} · {{ g.legs.length }} leg{{ g.legs.length === 1 ? '' : 's' }}<template v-if="span(g.first_ts, g.last_ts)"> · span {{ span(g.first_ts, g.last_ts) }}</template>
                    · outcome from final leg ({{ g.status || '—' }})
                  </p>
                  <FaxLegList v-if="g.legs.length" :legs="g.legs" />
                  <p v-else class="muted">No call legs recorded for this job.</p>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
        <p v-if="!groups.length" class="muted">No fax jobs match.</p>
        <div v-if="total > pageSize" class="inline" style="margin-top:10px; align-items:center">
          <button class="secondary" :disabled="page === 0" @click="prevPage">← Prev</button>
          <span class="muted">Page {{ page + 1 }} of {{ Math.ceil(total / pageSize) }} ({{ total }} jobs)</span>
          <button class="secondary" :disabled="(page + 1) * pageSize >= total" @click="nextPage">Next →</button>
        </div>
      </template>

      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
