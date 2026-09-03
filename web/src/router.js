import { createRouter, createWebHistory } from 'vue-router'

// createWebHistory y no hash: el binario sirve index.html para cualquier ruta que no sea
// /api ni /ws, así que las URL limpias funcionan al recargar.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'panel', component: () => import('@/pages/Panel.vue') },
    { path: '/creditos', name: 'creditos', component: () => import('@/pages/Creditos.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
