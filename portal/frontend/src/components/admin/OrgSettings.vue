<script setup lang="ts">
// Org-level settings: fax retention. Inbound faxes (incl. their stored PDFs)
// older than the retention period are permanently deleted by the hourly
// sweeper. 0 = keep indefinitely. Metadata (jobs, results) is always kept.
import { onMounted, ref } from 'vue'
import { api } from '../../api'

interface Org {
  id: number
  name: string
  retention_days: number
}

const props = defineProps<{ orgId: number }>()

const days = ref(30)
const saving = ref(false)
const saved = ref(false)
const error = ref('')

onMounted(async () => {
  try {
    const orgs = await api<Org[]>('/admin/orgs')
    const org = orgs.find((o) => o.id === props.orgId)
    if (org) days.value = org.retention_days
  } catch (e: any) { error.value = e.message }
})

async function save() {
  error.value = ''
  saved.value = false
  const v = Math.trunc(Number(days.value))
  if (!Number.isFinite(v) || v < 0 || v > 3650) {
    error.value = 'Retention must be 0 (keep forever) or between 1 and 3650 days.'
    return
  }
  saving.value = true
  try {
    await api(`/admin/orgs/${props.orgId}`, { method: 'PUT', json: { retention_days: v } })
    days.value = v
    saved.value = true
  } catch (e: any) { error.value = e.message } finally { saving.value = false }
}
</script>

<template>
  <div class="panel">
    <h2>Fax retention</h2>
    <p class="muted" style="margin-top:4px">
      Inbound faxes — including their stored PDF files — are permanently deleted once they are
      older than this many days. Job history and fax results are kept regardless.
    </p>
    <form style="margin-top:14px; max-width:320px" @submit.prevent="save">
      <label for="retention-days">Keep inbound faxes for (days)</label>
      <input id="retention-days" v-model="days" type="number" min="0" max="3650" step="1" required />
      <p class="muted" style="margin-top:6px; font-size:12px">
        0 = keep forever · default 30 · max 3650
      </p>
      <button type="submit" :disabled="saving" style="margin-top:10px">
        {{ saving ? 'Saving…' : 'Save' }}
      </button>
      <span v-if="saved" class="badge success" style="margin-left:10px">Saved</span>
    </form>
    <p v-if="error" class="error">{{ error }}</p>
  </div>
</template>
