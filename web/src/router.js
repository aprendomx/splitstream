import { createRouter, createWebHistory } from 'vue-router'

// createWebHistory y no hash: el binario sirve index.html para cualquier ruta que no sea
// /api ni /ws, así que las URL limpias funcionan al recargar.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'panel', component: () => import('@/pages/Panel.vue') },
    { path: '/creditos', name: 'creditos', component: () => import('@/pages/Creditos.vue') },
    { path: '/ajustes', name: 'ajustes', component: () => import('@/pages/Ajustes.vue') },
    { path: '/grabaciones', name: 'grabaciones', component: () => import('@/pages/Grabaciones.vue') },
    { path: '/historial', name: 'historial', component: () => import('@/pages/Historial.vue') },
    // El id viaja como prop (una cadena: lo convierte la página) para que Sesion.vue no
    // tenga que leer la ruta y se pueda montar desde cualquier sitio.
    { path: '/historial/:id', name: 'sesion', component: () => import('@/pages/Sesion.vue'), props: true },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
