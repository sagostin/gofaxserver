import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { useBrandingStore } from './branding'
import './style.css'

const app = createApp(App).use(createPinia()).use(router)
// Branding is public (no auth needed) so the login page is whitelabelled too.
useBrandingStore().fetch()
app.mount('#app')
