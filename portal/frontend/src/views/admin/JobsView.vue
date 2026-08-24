<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Org { id: number; name: string }
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
interface Row {
  result_type: string
  attempt_number: number
  success: boolean
  transferred_pages: number
  hangup_cause: string
  result_text: string
}

const jobs = ref<Job[]>([])
const orgs = ref<Org[]>([])
const orgFilter = ref(0)
const statusFilter = ref('')
const error = ref('')
const liveRows = ref<Row[] | null>(null)

async function load() {
  try {
    const qs = new URLSearchParams()
    if (orgFilter.value) qs.set('org_id', String(orgFilter.value))
    if (statusFilter.value) qs.set('status', statusFilter.value)
    jobs.value = await api<Job[]>('/admin/jobs?' + qs.toString())
    orgs.value = await api<Org[]>('/admin/orgs')
  } catch (e: any) { error.value = e.message }
}
onMounted(load)

async function live(j: Job) {
  liveRows.value = null
  try { liveRows.value = await api<Row[]>(`/admin/jobs/${j.id}/live`) } catch (e: any) { error.value = e.message }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>All fax jobs</h2>
      <form class="inline" @submit.prevent="load">
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
    </div>

    <div class="panel" v-if="liveRows">
      <h3>Upstream attempts (live)</h3>
      <table>
        <thead><tr><th>#</th><th>Type</th><th>Success</th><th>Pages</th><th>Cause</th><th>Result</th></tr></thead>
        <tbody>
          <tr v-for="(r, i) in liveRows" :key="i">
            <td>{{ r.attempt_number }}</td><td>{{ r.result_type }}</td><td>{{ r.success ? 'yes' : 'no' }}</td>
            <td>{{ r.transferred_pages }}</td><td>{{ r.hangup_cause }}</td><td>{{ r.result_text }}</td>
          </tr>
        </tbody>
      </table>
      <button class="secondary" style="margin-top:8px" @click="liveRows = null">Close</button>
    </div>

    <div class="panel">
      <table>
        <thead>
          <tr><th>Submitted</th><th>User / Org</th><th>Doc</th><th>From → To</th><th>Status</th><th>Pages</th><th>Att.</th><th>Result</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="j in jobs" :key="j.id">
            <td>{{ new Date(j.submitted_at).toLocaleString() }}</td>
            <td>{{ j.username }} / {{ j.org_name }}</td>
            <td>{{ j.original_filename }}</td>
            <td>{{ j.caller_number }} → {{ j.callee_number }}</td>
            <td><span class="badge" :class="j.status">{{ j.status }}</span></td>
            <td>{{ j.pages || '—' }}</td>
            <td>{{ j.attempts }}</td>
            <td>{{ j.result_text || j.last_error || '—' }}</td>
            <td><button class="secondary" @click="live(j)">Live</button></td>
          </tr>
        </tbody>
      </table>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </main>
</template>
