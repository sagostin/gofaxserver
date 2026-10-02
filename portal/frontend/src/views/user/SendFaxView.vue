<script setup lang="ts">
import { computed, ref } from 'vue'
import { upload, api } from '../../api'
import { useAuthStore } from '../../auth'

const auth = useAuthStore()
const numbers = computed(() => auth.me?.numbers || [])
const caller = ref(numbers.value[0]?.number || '')
const callee = ref('')
const fileEl = ref<HTMLInputElement | null>(null)
const busy = ref(false)
const error = ref('')
const okMsg = ref('')

// Conversion options
const fitMode = ref('constrain')
const coverEnabled = ref(false)
const coverTo = ref('')
const coverFrom = ref('')
const coverSubject = ref('')
const coverComments = ref('')

// Prepared state (two-step flow: prepare → preview → send)
interface Prepared {
  id: string
  filename: string
  pages: number
  previews: string[]
  expires_at: string
}
const prepared = ref<Prepared | null>(null)
const pageIdx = ref(0)

const imageExts = ['.png', '.jpg', '.jpeg']
const selectedFile = computed(() => fileEl.value?.files?.[0] || null)
const isImage = computed(() => {
  const n = selectedFile.value?.name.toLowerCase() || ''
  return imageExts.some((e) => n.endsWith(e))
})
const previewSrc = computed(() =>
  prepared.value ? '/portal/api' + prepared.value.previews[pageIdx.value] : ''
)

function resetPrepared() {
  prepared.value = null
  pageIdx.value = 0
}

async function prepare() {
  error.value = ''
  okMsg.value = ''
  const f = selectedFile.value
  if (!f) return (error.value = 'choose a file (PDF, TIFF, PNG, JPEG, DOCX or DOC)')
  if (!caller.value) return (error.value = 'no outbound number selected')
  const form = new FormData()
  form.append('file', f)
  form.append('fit_mode', fitMode.value)
  if (coverEnabled.value) {
    form.append('cover_enabled', 'true')
    form.append('cover_to', coverTo.value || callee.value)
    form.append('cover_from', coverFrom.value || caller.value)
    form.append('cover_subject', coverSubject.value)
    form.append('cover_comments', coverComments.value)
  }
  busy.value = true
  try {
    prepared.value = await upload<Prepared>('/faxes/prepare', form)
    pageIdx.value = 0
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

async function send() {
  error.value = ''
  okMsg.value = ''
  if (!prepared.value) return (error.value = 'prepare a preview first')
  const form = new FormData()
  form.append('prepared_id', prepared.value.id)
  form.append('caller_number', caller.value)
  form.append('callee_number', callee.value)
  busy.value = true
  try {
    const job = await upload<any>('/faxes', form)
    okMsg.value = `Fax submitted — job ${job.job_uuid}. Track it under My Faxes.`
    callee.value = ''
    coverTo.value = ''
    coverSubject.value = ''
    coverComments.value = ''
    if (fileEl.value) fileEl.value.value = ''
    resetPrepared()
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

async function discard() {
  if (prepared.value) {
    try {
      await api(`/faxes/prepare/${prepared.value.id}`, { method: 'DELETE' })
    } catch {
      /* already expired/gone — fine */
    }
  }
  resetPrepared()
}
</script>

<template>
  <main class="page">
    <div class="panel">
      <h2>Send a fax</h2>
      <form @submit.prevent="prepared ? send() : prepare()">
        <div>
          <label>From (your outbound numbers)</label>
          <select v-model="caller" :disabled="!!prepared">
            <option v-for="n in numbers" :key="n.id" :value="n.number">
              {{ n.number }}{{ n.name ? ` (${n.name})` : '' }}
            </option>
          </select>
        </div>
        <div>
          <label>To (destination fax number)</label>
          <input v-model="callee" placeholder="+1 555 987 6543" required :disabled="!!prepared" />
        </div>
        <div>
          <label>Document (PDF, TIFF, PNG, JPEG, DOCX or DOC — max ~20MB)</label>
          <input
            ref="fileEl"
            type="file"
            accept=".pdf,.tif,.tiff,.png,.jpg,.jpeg,.docx,.doc,application/pdf,image/tiff,image/png,image/jpeg"
            required
            :disabled="!!prepared"
            @change="resetPrepared"
          />
        </div>
        <div v-if="isImage && !prepared">
          <label>Image fit on page</label>
          <select v-model="fitMode">
            <option value="constrain">Fit inside page (keeps proportions, white margins)</option>
            <option value="fill">Fill page (keeps proportions, crops overflow)</option>
            <option value="stretch">Stretch to fill page (may distort)</option>
          </select>
        </div>
        <div v-if="!prepared">
          <label class="inline" style="align-items: center">
            <input type="checkbox" v-model="coverEnabled" style="width: auto" />
            Add a cover page
          </label>
        </div>
        <template v-if="coverEnabled && !prepared">
          <div>
            <label>Cover: To</label>
            <input v-model="coverTo" :placeholder="callee || 'recipient name/number'" />
          </div>
          <div>
            <label>Cover: From</label>
            <input v-model="coverFrom" :placeholder="caller || 'your name/number'" />
          </div>
          <div>
            <label>Cover: Subject</label>
            <input v-model="coverSubject" placeholder="optional" />
          </div>
          <div>
            <label>Cover: Comments</label>
            <input v-model="coverComments" placeholder="optional" />
          </div>
        </template>

        <div v-if="prepared" class="preview">
          <p class="muted">
            Preview of <b>{{ prepared.filename }}</b> — {{ prepared.pages }} page(s). This is the
            black-and-white fax the recipient will get.
          </p>
          <div v-if="prepared.previews.length" class="preview-nav inline">
            <button type="button" class="secondary" :disabled="pageIdx === 0" @click="pageIdx--">
              ‹ Prev
            </button>
            <span>Page {{ pageIdx + 1 }} / {{ prepared.previews.length }}</span>
            <button
              type="button"
              class="secondary"
              :disabled="pageIdx >= prepared.previews.length - 1"
              @click="pageIdx++"
            >
              Next ›
            </button>
          </div>
          <img v-if="prepared.previews.length" :src="previewSrc" class="preview-img" alt="fax preview" />
          <p v-else class="muted">Preview unavailable on this server — the document is still ready to send.</p>
        </div>

        <div class="inline">
          <button v-if="!prepared" :disabled="busy" type="submit">
            {{ busy ? 'Converting…' : 'Prepare preview' }}
          </button>
          <template v-else>
            <button :disabled="busy" type="submit">{{ busy ? 'Sending…' : 'Send fax' }}</button>
            <button type="button" class="secondary" :disabled="busy" @click="discard">Discard</button>
          </template>
        </div>
      </form>
      <p v-if="okMsg" class="okmsg">{{ okMsg }}</p>
      <p v-if="error" class="error">{{ error }}</p>
    </div>
    <p class="muted">A PDF report is emailed to your account address when the fax finishes — whether it succeeded or failed.</p>
  </main>
</template>

<style scoped>
.preview {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px;
  margin: 8px 0;
}
.preview-nav {
  align-items: center;
  margin-bottom: 8px;
}
.preview-img {
  width: 100%;
  max-width: 480px;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: #fff;
  display: block;
}
</style>
