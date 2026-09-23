import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from './auth'

const router = createRouter({
  history: createWebHistory('/portal/'),
  routes: [
    { path: '/login', component: () => import('./views/LoginView.vue') },
    // user realm
    { path: '/app', component: () => import('./views/user/SendFaxView.vue'), meta: { role: 'user' } },
    { path: '/app/faxes', component: () => import('./views/user/JobsView.vue'), meta: { role: 'user' } },
    { path: '/app/inbox', component: () => import('./views/user/InboxView.vue'), meta: { role: 'user' } },
    // admin realm
    { path: '/admin', component: () => import('./views/admin/OrgsView.vue'), meta: { role: 'admin' } },
    { path: '/admin/numbers', component: () => import('./views/admin/NumbersView.vue'), meta: { role: 'admin' } },
    { path: '/admin/users', component: () => import('./views/admin/UsersView.vue'), meta: { role: 'admin' } },
    { path: '/admin/endpoints', component: () => import('./views/admin/EndpointsView.vue'), meta: { role: 'admin' } },
    { path: '/admin/gateways', component: () => import('./views/admin/GatewaysView.vue'), meta: { role: 'admin' } },
    { path: '/admin/templates', component: () => import('./views/admin/TemplatesView.vue'), meta: { role: 'admin' } },
    { path: '/admin/dialplan', component: () => import('./views/admin/DialplanView.vue'), meta: { role: 'admin' } },
    { path: '/admin/fax-policies', component: () => import('./views/admin/FaxPoliciesView.vue'), meta: { role: 'admin' } },
    { path: '/admin/active', component: () => import('./views/admin/ActiveFaxesView.vue'), meta: { role: 'admin' } },
    { path: '/admin/jobs', component: () => import('./views/admin/JobsView.vue'), meta: { role: 'admin' } },
    { path: '/admin/inbound', component: () => import('./views/admin/InboundView.vue'), meta: { role: 'admin' } },
    { path: '/admin/audit', component: () => import('./views/admin/AuditView.vue'), meta: { role: 'admin' } },
    { path: '/', redirect: '/login' },
  ],
})

let meLoaded = false

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!meLoaded) {
    await auth.fetchMe()
    meLoaded = true
  }
  if (!to.path.startsWith('/login')) {
    if (!auth.me) return '/login'
    if (to.meta.role && to.meta.role !== auth.me.role) {
      return auth.me.role === 'admin' ? '/admin' : '/app'
    }
  }
  return true
})

export default router
