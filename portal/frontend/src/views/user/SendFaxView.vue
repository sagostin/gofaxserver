<script setup lang="ts">
import { computed, ref } from 'vue'
import { upload } from '../../api'
import { useAuthStore } from '../../auth'

const auth = useAuthStore()
const numbers = computed(() => auth.me?.numbers || [])
const caller = ref(numbers.value[0]?.number || '')
const callee = ref('')
const fileEl = ref<HTMLInputElement | null>(null)
const busy = ref(false)
const error = ref('')
const okMsg = ref('')

async function send() {
  error.value = ''
  okMsg.value = ''
  const f = fileEl.value?.files?.[0]
  if (!f) return (error.value = 'choose a PDF or TIFF file')
  if (!caller.value) return (error.value = 'no outbound number selected')
  const form = new FormData()
  form.append('file', f)
  form.append('caller_number', caller.value)
  form.append('callee_number', callee.value)
  busy.value = true
  try {
    const job = await upload<any>('/faxes', form)
    okMsg.value = `Fax submitted — job ${job.job_uuid}. Track it under My Faxes.`
    callee.value = ''
    if (fileEl.value) fileEl.value.value = ''
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Send a fax</h2>
      <form @submit.prevent="send">
        <div>
          <label>From (your outbound numbers)</label>
          <select v-model="caller">
            <option v-for="n in numbers" :key="n.id" :value="n.number">
              {{ n.number }}{{ n.name ? ` (${n.name})` : '' }}
            </option>
          </select>
        </div>
        <div>
          <label>To (destination fax number)</label>
          <input v-model="callee" placeholder="+1 555 987 6543" required />
        </div>
        <div>
          <label>Document (PDF / TIFF, max ~20MB)</label>
          <input ref="fileEl" type="file" accept=".pdf,.tif,.tiff,application/pdf,image/tiff" required />
        </div>
        <button :disabled="busy" type="submit">{{ busy ? 'Sending…' : 'Send fax' }}</button>
      </form>
      <p v-if="okMsg" class="okmsg">{{ okMsg }}</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
    <p class="muted">A PDF report is emailed to your account address when the fax finishes — whether it succeeded or failed.</p>
  </main>
</template>
