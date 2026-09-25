<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, upload } from '../../api'
import { useBrandingStore } from '../../branding'

const branding = useBrandingStore()

const name = ref('')
const accentColor = ref('')
const error = ref('')
const okmsg = ref('')
const busy = ref(false)

const logoInput = ref<HTMLInputElement | null>(null)
const faviconInput = ref<HTMLInputElement | null>(null)

// Preview URLs include the cache-buster returned by the API so a fresh upload
// is visible immediately.
const logoPreview = ref('')
const faviconPreview = ref('')

async function load() {
  await branding.fetch()
  name.value = branding.name
  accentColor.value = branding.accentColor
  logoPreview.value = branding.logoUrl
  faviconPreview.value = branding.faviconUrl
}
onMounted(load)

async function saveText() {
  error.value = ''
  okmsg.value = ''
  busy.value = true
  try {
    await api('/admin/branding', {
      method: 'PUT',
      json: { name: name.value, accent_color: accentColor.value },
    })
    await load()
    okmsg.value = 'Branding saved.'
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function pickImage(kind: 'logo' | 'favicon', ev: Event) {
  const input = ev.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // allow re-picking the same file
  if (!file) return
  error.value = ''
  okmsg.value = ''
  busy.value = true
  try {
    const form = new FormData()
    form.append('file', file)
    await upload(`/admin/branding/${kind}`, form)
    await load()
    okmsg.value = kind === 'logo' ? 'Logo updated.' : 'Favicon updated.'
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}

async function removeImage(kind: 'logo' | 'favicon') {
  if (!confirm(`Remove the ${kind}? The portal falls back to the default look.`)) return
  error.value = ''
  okmsg.value = ''
  busy.value = true
  try {
    await api(`/admin/branding/${kind}`, { method: 'DELETE' })
    await load()
    okmsg.value = kind === 'logo' ? 'Logo removed.' : 'Favicon removed.'
  } catch (e: any) { error.value = e.message } finally { busy.value = false }
}
</script>

<template>
  <main class="page">
    <h2>Branding</h2>

    <div class="panel">
      <p class="muted">
        Whitelabel the portal: the name and logo appear in the navbar, on the login page,
        and in the browser tab. Changes apply immediately for all users.
      </p>
      <form class="inline" @submit.prevent="saveText">
        <div>
          <label>Portal name</label>
          <input v-model="name" maxlength="80" required style="min-width:220px" />
        </div>
        <div>
          <label>Accent color (empty = default)</label>
          <input :value="accentColor || '#2563eb'" type="color" style="width:52px;height:35px;padding:2px"
                 @input="accentColor = ($event.target as HTMLInputElement).value" />
        </div>
        <div>
          <label>&nbsp;</label>
          <button class="secondary" type="button" :disabled="!accentColor" @click="accentColor = ''">Reset color</button>
        </div>
        <div>
          <label>&nbsp;</label>
          <button :disabled="busy" type="submit">{{ busy ? '…' : 'Save' }}</button>
        </div>
      </form>
    </div>

    <div class="panel">
      <h2>Navbar logo</h2>
      <div style="display:flex;align-items:center;gap:16px;flex-wrap:wrap">
        <img v-if="logoPreview" :src="logoPreview" alt="Current logo" class="brand-preview" />
        <span v-else class="muted">No logo set — the portal name is shown as text.</span>
        <input ref="logoInput" type="file" accept="image/png,image/jpeg,image/gif,image/webp"
               style="display:none" @change="pickImage('logo', $event)" />
        <button type="button" class="secondary" :disabled="busy" @click="logoInput?.click()">
          {{ logoPreview ? 'Replace' : 'Upload' }}
        </button>
        <button v-if="logoPreview" class="danger" :disabled="busy" @click="removeImage('logo')">Remove</button>
      </div>
      <p class="muted" style="margin-bottom:0">PNG, JPEG, GIF or WebP, max 1 MiB. Rendered at 28px height in the navbar.</p>
    </div>

    <div class="panel">
      <h2>Favicon</h2>
      <div style="display:flex;align-items:center;gap:16px;flex-wrap:wrap">
        <img v-if="faviconPreview" :src="faviconPreview" alt="Current favicon" class="favicon-preview" />
        <span v-else class="muted">No favicon set — the browser default is used.</span>
        <input ref="faviconInput" type="file" accept="image/png,image/jpeg,image/gif,image/webp,image/x-icon"
               style="display:none" @change="pickImage('favicon', $event)" />
        <button type="button" class="secondary" :disabled="busy" @click="faviconInput?.click()">
          {{ faviconPreview ? 'Replace' : 'Upload' }}
        </button>
        <button v-if="faviconPreview" class="danger" :disabled="busy" @click="removeImage('favicon')">Remove</button>
      </div>
      <p class="muted" style="margin-bottom:0">PNG, JPEG, GIF, WebP or ICO, max 1 MiB. Square images work best.</p>
    </div>

    <p v-if="error" class="error">{{ error }}</p>
    <p v-if="okmsg" class="okmsg">{{ okmsg }}</p>
  </main>
</template>
