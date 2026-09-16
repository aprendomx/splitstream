<script setup>
import { iAjustes, iBroadcast, iDesplegar, iGrabaciones, iHistorial, iInfo, iOcultar, iSalir, iVer } from '@/iconos'
import { ref, computed, onMounted } from 'vue'
import { usePanel } from '@/stores/panel'
import Asistente from '@/components/Asistente.vue'
import { ApiError } from '@/api'
import { t, idioma, cambiarIdioma, idiomas } from '@/i18n'

const panel = usePanel()
const password = ref('')
const verPassword = ref(false)
const errorLogin = ref(null)
const entrando = ref(false)

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
  } finally {
    entrando.value = false
  }
}
</script>

<template>
  <q-layout view="hHh lpR fFf">
    <q-header elevated class="bg-dark">
      <q-toolbar>
        <q-icon :name="iBroadcast" size="24px" class="q-mr-sm text-primary" />
        <q-toolbar-title class="text-weight-medium">Splitstream</q-toolbar-title>
        <q-btn-dropdown flat dense no-caps :label="idioma.toUpperCase()" :aria-label="t('app.idioma')" :dropdown-icon="iDesplegar">
          <q-list>
            <q-item
              v-for="l in idiomas" :key="l.id" clickable v-close-popup
              :active="l.id === idioma"
              :aria-current="l.id === idioma ? 'true' : undefined"
              @click="cambiarIdioma(l.id)"
            >
              <q-item-section>{{ l.nombre }}</q-item-section>
            </q-item>
          </q-list>
        </q-btn-dropdown>
        <q-btn v-if="panel.autenticado" flat round dense :icon="iHistorial" :aria-label="t('app.historial')" :to="{ name: 'historial' }" />
        <q-btn v-if="panel.autenticado" flat round dense :icon="iGrabaciones" :aria-label="t('app.grabaciones')" :to="{ name: 'grabaciones' }" />
        <q-btn v-if="panel.autenticado" flat round dense :icon="iAjustes" :aria-label="t('app.ajustes')" :to="{ name: 'ajustes' }" />
        <q-btn
          v-if="panel.autenticado"
          flat round dense :icon="iInfo"
          :aria-label="t('app.creditos')"
          :to="{ name: 'creditos' }"
        />
        <q-btn v-if="panel.autenticado" flat round dense :icon="iSalir"
               :aria-label="t('app.cerrar_sesion')" @click="panel.salir()" />
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
           entre los dos es peor que esperar medio segundo. -->
      <q-page v-if="panel.cargando" class="flex flex-center">
        <q-spinner size="32px" color="primary" />
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
        <q-card flat bordered style="width: 340px; max-width: 100%">
          <q-card-section class="q-gutter-md">
            <div class="text-h6">{{ t('app.entrar') }}</div>
            <q-form @submit.prevent="entrar" class="q-gutter-md">
              <q-input
                v-model="password"
                :label="t('comun.contrasena')"
                :type="verPassword ? 'text' : 'password'"
                outlined
                dense
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
