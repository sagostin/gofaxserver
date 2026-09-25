<script setup lang="ts">
import { ref } from 'vue'
import { useAuthStore } from '../auth'
import router from '../router'

const auth = useAuthStore()
const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

// step: 'credentials' | 'verify' | 'enroll'
const step = ref<'credentials' | 'verify' | 'enroll'>('credentials')
const mfaToken = ref('')
const code = ref('')
const enrollment = ref<{ secret: string; otpauth_url: string; qr_png: string } | null>(null)

function gotoHome() {
  router.push(auth.me?.role === 'admin' ? '/admin' : '/app')
}

async function submit() {
  error.value = ''
  busy.value = true
  try {
    const res = await auth.login(username.value, password.value)
    if ('mfa' in res) {
      mfaToken.value = res.mfa_token
      password.value = ''
      if (res.mfa === 'enroll_required') {
        enrollment.value = await auth.fetchTOTPSetup(res.mfa_token)
        step.value = 'enroll'
      } else {
        step.value = 'verify'
      }
      return
    }
    gotoHome()
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

async function submitCode() {
  error.value = ''
  busy.value = true
  try {
    await auth.verifyTOTP(mfaToken.value, code.value)
    gotoHome()
  } catch (e: any) {
    error.value = e.message
    code.value = ''
  } finally {
    busy.value = false
  }
}

function restart() {
  step.value = 'credentials'
  mfaToken.value = ''
  code.value = ''
  enrollment.value = null
  error.value = ''
}
</script>

<template>
  <div class="login-wrap">
    <div class="panel">
      <h2>Fax Portal</h2>

      <form v-if="step === 'credentials'" @submit.prevent="submit">
        <div style="margin-bottom: 8px">
          <label>Username</label>
          <input v-model="username" autocomplete="username" required autofocus />
        </div>
        <div style="margin-bottom: 12px">
          <label>Password</label>
          <input v-model="password" type="password" autocomplete="current-password" required />
        </div>
        <button :disabled="busy" type="submit">{{ busy ? '…' : 'Log in' }}</button>
      </form>

      <form v-else-if="step === 'verify'" @submit.prevent="submitCode">
        <p class="muted">Enter the 6-digit code from your authenticator app.</p>
        <div style="margin-bottom: 12px">
          <label>Authentication code</label>
          <input v-model="code" inputmode="numeric" pattern="[0-9]{6}" maxlength="6"
                 autocomplete="one-time-code" required autofocus />
        </div>
        <button :disabled="busy" type="submit">{{ busy ? '…' : 'Verify' }}</button>
        <button class="secondary" type="button" style="margin-left:8px" @click="restart">Back</button>
      </form>

      <form v-else @submit.prevent="submitCode">
        <p>Your organization requires two-factor authentication. Scan this QR code with your
           authenticator app (e.g. Google Authenticator, 1Password, Authy), then enter the
           6-digit code to finish setup.</p>
        <div v-if="enrollment" style="text-align:center;margin-bottom:12px">
          <img :src="enrollment.qr_png" alt="TOTP QR code" width="192" height="192" />
          <details style="margin-top:8px;text-align:left">
            <summary class="muted">Can't scan? Enter the key manually</summary>
            <code style="word-break:break-all">{{ enrollment.secret }}</code>
          </details>
        </div>
        <div style="margin-bottom: 12px">
          <label>Authentication code</label>
          <input v-model="code" inputmode="numeric" pattern="[0-9]{6}" maxlength="6"
                 autocomplete="one-time-code" required autofocus />
        </div>
        <button :disabled="busy" type="submit">{{ busy ? '…' : 'Enable 2FA & log in' }}</button>
        <button class="secondary" type="button" style="margin-left:8px" @click="restart">Back</button>
      </form>

      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </div>
</template>
