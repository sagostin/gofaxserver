<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../../api'
import { useAuthStore } from '../auth'
import router from '../router'

const auth = useAuthStore()
const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

async function submit() {
  error.value = ''
  busy.value = true
  try {
    const me = await auth.login(username.value, password.value)
    router.push(me.role === 'admin' ? '/admin' : '/app')
  } catch (e: any) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <div class="panel">
      <h2>Fax Portal</h2>
      <form @submit.prevent="submit">
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
      <p v-if="error" class="error">{{ error }}</p>
    </div>
  </div>
</template>
