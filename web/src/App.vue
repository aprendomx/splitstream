<script setup>
import { iAjustes, iBroadcast, iDesplegar, iGrabaciones, iHistorial, iInfo, iMasOpciones, iOcultar, iOk, iPanel, iSalir, iTraducir, iVer } from '@/iconos'
import { ref, computed, nextTick, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { usePanel } from '@/stores/panel'
import Asistente from '@/components/Asistente.vue'
import { ApiError } from '@/api'
import { t, idioma, cambiarIdioma, idiomas } from '@/i18n'

const panel = usePanel()
const route = useRoute()
const password = ref('')
const passwordRef = ref(null)
const verPassword = ref(false)
const errorLogin = ref(null)
const entrando = ref(false)

// Nombre completo del idioma activo: aparece como etiqueta del menú (≥ 600 px) y en su
// aria-label, tanto con etiqueta visible como sin ella (móvil).
const nombreIdioma = computed(() => idiomas.find((l) => l.id === idioma.value)?.nombre)

onMounted(() => panel.cargar())

// El aviso de versión se cierra por versión: si sale otra, vuelve a aparecer.
const CLAVE_AVISO = 'splitstream.aviso-version-cerrado'
const avisoCerrado = ref(leerAvisoCerrado())
function leerAvisoCerrado() {
  try { return localStorage.getItem(CLAVE_AVISO) } catch { return null }
}
const avisoVersion = computed(() => {
  const u = panel.actualizacion
  if (!panel.autenticado || !u?.available || avisoCerrado.value === u.latest) return null
  // La URL viene de la respuesta de GitHub a través de la API: se acepta solo si es una
  // cadena y empieza por https://. Cualquier otra cosa (un javascript:, un http:// o un
  // null) se queda sin botón «Ver» en vez de convertirse en un enlace del panel.
  const url = typeof u.url === 'string' && u.url.startsWith('https://') ? u.url : null
  return { ...u, url }
})
function cerrarAviso() {
  avisoCerrado.value = panel.actualizacion?.latest ?? null
  try { localStorage.setItem(CLAVE_AVISO, avisoCerrado.value) } catch { /* sin almacenamiento, se repite al recargar */ }
}

async function entrar() {
  entrando.value = true
  errorLogin.value = null
  try {
    await panel.entrar(password.value)
    password.value = ''
  } catch (e) {
    errorLogin.value = e instanceof ApiError ? e.message : t('errores.no_se_pudo_entrar')
    // El foco vuelve al campo: quien lee con lector de pantalla oye el error y ya está
    // sobre el campo a corregir, sin tener que buscarlo.
    await nextTick()
    passwordRef.value?.focus()
  } finally {
    entrando.value = false
  }
}
</script>

<template>
  <q-layout view="hHh lpR fFf">
    <q-header elevated class="bg-dark">
      <q-toolbar class="barra">
        <q-icon :name="iBroadcast" size="24px" class="q-mr-sm text-primary" aria-hidden="true" />
        <q-toolbar-title class="ss-t-18">Splitstream</q-toolbar-title>

        <!-- Autenticado: pestañas de navegación. Sin sesión: solo el espacio, para que el
             menú de idioma quede a la derecha igual que en la pantalla de entrada. -->
        <!-- :model-value="route.name": QTabs trae su propia detección automática de la
             pestaña activa por ruta, pero solo la recalcula cuando cambia route.fullPath;
             en la carga inicial de «/» esa comprobación corre antes de que el router
             termine de resolver, y como la ruta no vuelve a cambiar se queda pegada en
             «ninguna activa». Fijar el modelo a route.name evita la carrera. -->
        <!-- Las cuatro pestañas con icono + etiqueta no caben a 375 px junto al logo, el
             idioma y «más»: por debajo de 480 px se quita la etiqueta visible (el icono y
             el aria-label bastan) para que quepan sin scroll horizontal. -->
        <q-tabs v-if="panel.autenticado" :model-value="route.name" dense no-caps narrow-indicator
                active-color="primary" indicator-color="primary" class="pestanas"
                :aria-label="t('app.navegacion')">
          <q-route-tab name="panel" :to="{ name: 'panel' }" :icon="iPanel"
                        :label="$q.screen.width >= 480 ? t('app.panel') : undefined"
                        :aria-label="t('app.panel')" exact />
          <q-route-tab name="historial" :to="{ name: 'historial' }" :icon="iHistorial"
                        :label="$q.screen.width >= 480 ? t('app.historial') : undefined"
                        :aria-label="t('app.historial')" />
          <q-route-tab name="grabaciones" :to="{ name: 'grabaciones' }" :icon="iGrabaciones"
                        :label="$q.screen.width >= 480 ? t('app.grabaciones') : undefined"
                        :aria-label="t('app.grabaciones')" />
          <q-route-tab name="ajustes" :to="{ name: 'ajustes' }" :icon="iAjustes"
                        :label="$q.screen.width >= 480 ? t('app.ajustes') : undefined"
                        :aria-label="t('app.ajustes')" />
        </q-tabs>
        <q-space v-else />

        <q-btn-dropdown flat dense no-caps :icon="iTraducir" :dropdown-icon="iDesplegar"
                        :label="$q.screen.gt.xs ? nombreIdioma : undefined"
                        :aria-label="t('app.idioma_actual', { nombre: nombreIdioma })" class="q-ml-sm">
          <q-list role="menu">
            <q-item v-for="l in idiomas" :key="l.id" clickable v-close-popup role="menuitemradio"
                    :active="l.id === idioma" :aria-checked="l.id === idioma" @click="cambiarIdioma(l.id)">
              <q-item-section>{{ l.nombre }}</q-item-section>
              <q-item-section v-if="l.id === idioma" side><q-icon :name="iOk" size="18px" /></q-item-section>
            </q-item>
          </q-list>
        </q-btn-dropdown>

        <q-btn-dropdown v-if="panel.autenticado" flat round dense :icon="iMasOpciones" :dropdown-icon="iDesplegar"
                        :aria-label="t('app.mas')" class="q-ml-xs">
          <q-list role="menu">
            <q-item clickable v-close-popup :to="{ name: 'creditos' }" role="menuitem">
              <q-item-section avatar><q-icon :name="iInfo" /></q-item-section>
              <q-item-section>{{ t('app.creditos') }}</q-item-section>
            </q-item>
            <q-separator />
            <q-item clickable v-close-popup role="menuitem" class="text-negative" @click="panel.salir()">
              <q-item-section avatar><q-icon :name="iSalir" /></q-item-section>
              <q-item-section>{{ t('app.cerrar_sesion') }}</q-item-section>
            </q-item>
          </q-list>
        </q-btn-dropdown>
      </q-toolbar>
    </q-header>

    <q-page-container>
      <q-banner v-if="avisoVersion" dense class="bg-primary text-white" role="status">
        {{ t('app.aviso_version', { version: avisoVersion.latest }) }}
        <template #action>
          <q-btn v-if="avisoVersion.url" flat no-caps :label="t('app.ver')" :href="avisoVersion.url" target="_blank" rel="noopener" />
          <q-btn flat no-caps :label="t('comun.cerrar')" @click="cerrarAviso" />
        </template>
      </q-banner>

      <!-- Mientras se sabe si hay sesión, no se enseña ni el login ni el panel: parpadear
           entre los dos es peor que esperar medio segundo. Un esqueleto de la forma que
           va a tomar la página es menos brusco que un spinner suelto. -->
      <q-page v-if="panel.cargando" aria-busy="true" class="q-pa-md">
        <span class="sr-only">{{ t('app.cargando') }}</span>
        <q-skeleton type="rect" height="120px" class="q-mb-md" />
        <q-skeleton type="rect" height="96px" class="q-mb-md" />
        <q-skeleton type="rect" height="96px" class="q-mb-md" />
        <q-skeleton type="rect" height="96px" />
      </q-page>

      <!-- Primer arranque: el asistente sustituye al login mientras no haya contraseña. -->
      <q-page v-else-if="panel.necesitaSetup" class="flex flex-center q-pa-md">
        <Asistente
          :pide-codigo="panel.pideCodigo"
          :local="panel.esLocal"
          @listo="panel.trasSetup()"
        />
      </q-page>

      <q-page v-else-if="!panel.autenticado" class="flex flex-center q-pa-md">
        <q-card flat bordered style="width: 420px; max-width: 100%">
          <q-card-section class="q-gutter-md">
            <div class="ss-t-22">{{ t('app.entrar') }}</div>
            <div class="ss-t-14 ss-muted">{{ t('app.entrar_ayuda') }}</div>
            <q-form @submit.prevent="entrar" class="q-gutter-md">
              <q-input
                ref="passwordRef"
                v-model="password"
                :label="t('comun.contrasena')"
                :type="verPassword ? 'text' : 'password'"
                outlined
                autofocus
                autocomplete="current-password"
              >
                <template #append>
                  <q-btn flat round dense :icon="verPassword ? iOcultar : iVer"
                         :aria-label="verPassword ? t('app.ocultar_contrasena') : t('app.mostrar_contrasena')"
                         @click="verPassword = !verPassword" />
                </template>
              </q-input>
              <q-banner v-if="errorLogin" dense class="bg-red-10 text-red-2 rounded-borders"
                        role="alert">
                {{ errorLogin }}
              </q-banner>
              <q-btn type="submit" unelevated no-caps color="primary" class="full-width"
                     :loading="entrando" :label="t('app.entrar')" />
            </q-form>
          </q-card-section>
        </q-card>
      </q-page>

      <router-view v-else />
    </q-page-container>
  </q-layout>
</template>

<style scoped lang="scss">
// Barra: alto mínimo cómodo para el logo + pestañas, con el mismo espaciado que el resto
// del panel (tokens del spec §3.1, nunca un número suelto).
.barra {
  min-height: 56px;
  gap: var(--ss-space-1);
}
.pestanas .q-tab {
  min-height: 44px;
  padding: 0 var(--ss-space-3);
}
// Por debajo de 768 px las pestañas apiladas (icono + etiqueta, por el modo dense) ocupan
// menos si el texto y el icono encogen un poco; las cuatro siguen cabiendo a 375 px.
@media (max-width: 767px) {
  .pestanas .q-tab__label {
    font-size: 12px;
  }
  .pestanas .q-tab__icon {
    font-size: 20px;
  }
}
</style>
