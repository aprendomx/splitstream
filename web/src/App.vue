<script setup>
import { iBroadcast, iOcultar, iSalir, iVer } from '@/iconos'
import { ref, onMounted } from 'vue'
import { usePanel } from '@/stores/panel'
import { ApiError } from '@/api'

const panel = usePanel()
const password = ref('')
const verPassword = ref(false)
const errorLogin = ref(null)
const entrando = ref(false)

onMounted(() => panel.cargar())

async function entrar() {
  entrando.value = true
  errorLogin.value = null
  try {
    await panel.entrar(password.value)
    password.value = ''
  } catch (e) {
    errorLogin.value = e instanceof ApiError ? e.message : 'No se pudo entrar'
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
        <q-btn v-if="panel.autenticado" flat round dense :icon="iSalir"
               aria-label="Cerrar sesión" @click="panel.salir()" />
      </q-toolbar>
    </q-header>

    <q-page-container>
      <!-- Mientras se sabe si hay sesión, no se enseña ni el login ni el panel: parpadear
           entre los dos es peor que esperar medio segundo. -->
      <q-page v-if="panel.cargando" class="flex flex-center">
        <q-spinner size="32px" color="primary" />
      </q-page>

      <q-page v-else-if="!panel.autenticado" class="flex flex-center q-pa-md">
        <q-card flat bordered style="width: 340px; max-width: 100%">
          <q-card-section class="q-gutter-md">
            <div class="text-h6">Entrar</div>
            <q-form @submit.prevent="entrar" class="q-gutter-md">
              <q-input
                v-model="password"
                label="Contraseña"
                :type="verPassword ? 'text' : 'password'"
                outlined
                dense
                autofocus
                autocomplete="current-password"
              >
                <template #append>
                  <q-btn flat round dense :icon="verPassword ? iOcultar : iVer"
                         :aria-label="verPassword ? 'Ocultar' : 'Mostrar'"
                         @click="verPassword = !verPassword" />
                </template>
              </q-input>
              <q-banner v-if="errorLogin" dense class="bg-red-10 text-red-2 rounded-borders"
                        role="alert">
                {{ errorLogin }}
              </q-banner>
              <q-btn type="submit" unelevated no-caps color="primary" class="full-width"
                     :loading="entrando" label="Entrar" />
            </q-form>
          </q-card-section>
        </q-card>
      </q-page>

      <router-view v-else />
    </q-page-container>
  </q-layout>
</template>
