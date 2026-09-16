<script setup>
import { ref, computed } from 'vue'
import { iVer, iOcultar, iBroadcast, iError, iInfo, iAviso } from '@/iconos'
import { api, ApiError } from '@/api'
import { t, idioma, cambiarIdioma, idiomas } from '@/i18n'

const props = defineProps({
  // Del GET /api/setup: si la petición no vino de la propia máquina, hace falta el código.
  pideCodigo: Boolean,
  local: Boolean,
})
const emit = defineEmits(['listo'])

const password = ref('')
const repetida = ref('')
const codigo = ref('')
const verPassword = ref(false)
const guardando = ref(false)
const error = ref(null)

// La única comprobación en el cliente: que las dos contraseñas coincidan. El resto lo valida
// el backend, que ya devuelve mensajes escritos para personas.
const noCoinciden = computed(
  () => repetida.value.length > 0 && password.value !== repetida.value,
)
const puedeSeguir = computed(
  () =>
    password.value.length >= 8 &&
    password.value === repetida.value &&
    (!props.pideCodigo || codigo.value.trim().length > 0),
)

async function configurar() {
  guardando.value = true
  error.value = null
  try {
    await api.configurar(password.value, codigo.value)
    emit('listo')
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : t('errores.no_se_pudo_configurar')
  } finally {
    guardando.value = false
  }
}
</script>

<template>
  <div class="asistente-envoltorio column items-center q-gutter-sm">
    <!-- Primera pantalla que ve una persona nueva: el selector va aquí, antes de la
         tarjeta de bienvenida, para que se pueda elegir el idioma antes de leer nada. -->
    <q-btn-toggle
      :model-value="idioma"
      @update:model-value="cambiarIdioma"
      no-caps
      dense
      unelevated
      toggle-color="primary"
      :options="idiomas.map((l) => ({ label: l.nombre, value: l.id }))"
      :aria-label="t('app.idioma')"
    />

    <q-card flat bordered class="asistente">
      <q-card-section class="text-center q-pb-none">
        <q-icon :name="iBroadcast" size="40px" class="text-primary" />
        <div class="ss-t-22 q-mt-sm">{{ t('asistente.bienvenida') }}</div>
        <div class="ss-t-14 ss-muted q-mt-xs">
          {{ t('asistente.subtitulo') }}
        </div>
      </q-card-section>

      <q-card-section class="q-gutter-md">
        <!-- El código solo aparece cuando de verdad hace falta: al abrir el panel desde otra
             máquina. En el PC de casa esta parte ni existe. -->
        <template v-if="pideCodigo">
          <q-banner dense class="bg-blue-10 text-blue-2 rounded-borders">
            <template #avatar><q-icon :name="iInfo" color="info" /></template>
            {{ t('asistente.aviso_codigo') }}
          </q-banner>
          <q-input
            v-model="codigo"
            :label="t('asistente.codigo_label')"
            placeholder="XXXX-XXXX-XXXX"
            outlined
            autocapitalize="characters"
            autocorrect="off"
            spellcheck="false"
            class="codigo"
          />
        </template>

        <q-input
          v-model="password"
          :label="t('comun.contrasena')"
          :type="verPassword ? 'text' : 'password'"
          :hint="t('asistente.hint_contrasena')"
          outlined
          autofocus
          autocomplete="new-password"
        >
          <template #append>
            <q-btn
              flat round dense
              :icon="verPassword ? iOcultar : iVer"
              :aria-label="verPassword ? t('asistente.ocultar_contrasena') : t('asistente.mostrar_contrasena')"
              @click="verPassword = !verPassword"
            />
          </template>
        </q-input>

        <q-input
          v-model="repetida"
          :label="t('asistente.repetir_contrasena')"
          :type="verPassword ? 'text' : 'password'"
          :error="noCoinciden"
          :error-message="t('asistente.no_coinciden')"
          outlined
          autocomplete="new-password"
          @keyup.enter="puedeSeguir && configurar()"
        />

        <!-- El aviso que pediste: si el panel es alcanzable desde fuera, esta contraseña es
             lo único que separa a cualquiera de tus claves de retransmisión. -->
        <q-banner v-if="!local" dense class="bg-orange-10 text-orange-2 rounded-borders">
          <template #avatar><q-icon :name="iAviso" color="warning" /></template>
          {{ t('asistente.aviso_red') }}
        </q-banner>

        <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
          <template #avatar><q-icon :name="iError" color="negative" /></template>
          {{ error }}
        </q-banner>
      </q-card-section>

      <q-card-actions class="q-px-md q-pb-md">
        <q-btn
          unelevated no-caps color="primary" class="full-width" size="md"
          :loading="guardando"
          :disable="!puedeSeguir"
          :label="t('asistente.empezar')"
          @click="configurar"
        />
      </q-card-actions>
    </q-card>
  </div>
</template>

<style scoped>
.asistente-envoltorio { width: 480px; max-width: 100%; }
.asistente { width: 100%; }
.codigo :deep(input) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
</style>
