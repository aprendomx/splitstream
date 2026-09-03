<script setup>
import { ref, computed } from 'vue'
import { iVer, iOcultar, iBroadcast, iError, iInfo, iAviso } from '@/iconos'
import { api, ApiError } from '@/api'

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
    error.value = e instanceof ApiError ? e.message : 'No se pudo configurar'
  } finally {
    guardando.value = false
  }
}
</script>

<template>
  <q-card flat bordered class="asistente">
    <q-card-section class="text-center q-pb-none">
      <q-icon :name="iBroadcast" size="40px" class="text-primary" />
      <div class="text-h6 q-mt-sm">Bienvenido a Splitstream</div>
      <div class="text-body2 text-grey-5 q-mt-xs">
        Elige una contraseña para el panel. Es lo único que hace falta para empezar.
      </div>
    </q-card-section>

    <q-card-section class="q-gutter-md">
      <!-- El código solo aparece cuando de verdad hace falta: al abrir el panel desde otra
           máquina. En el PC de casa esta parte ni existe. -->
      <template v-if="pideCodigo">
        <q-banner dense class="bg-blue-10 text-blue-2 rounded-borders">
          <template #avatar><q-icon :name="iInfo" color="info" /></template>
          Estás abriendo el panel desde otro equipo, así que hace falta el código que
          Splitstream imprimió en su consola al arrancar.
        </q-banner>
        <q-input
          v-model="codigo"
          label="Código de la consola"
          placeholder="XXXX-XXXX-XXXX"
          outlined
          dense
          autocapitalize="characters"
          autocorrect="off"
          spellcheck="false"
          class="codigo"
        />
      </template>

      <q-input
        v-model="password"
        label="Contraseña"
        :type="verPassword ? 'text' : 'password'"
        hint="Mínimo 8 caracteres"
        outlined
        dense
        autofocus
        autocomplete="new-password"
      >
        <template #append>
          <q-btn
            flat round dense
            :icon="verPassword ? iOcultar : iVer"
            :aria-label="verPassword ? 'Ocultar la contraseña' : 'Mostrar la contraseña'"
            @click="verPassword = !verPassword"
          />
        </template>
      </q-input>

      <q-input
        v-model="repetida"
        label="Repite la contraseña"
        :type="verPassword ? 'text' : 'password'"
        :error="noCoinciden"
        error-message="No coinciden"
        outlined
        dense
        autocomplete="new-password"
        @keyup.enter="puedeSeguir && configurar()"
      />

      <!-- El aviso que pediste: si el panel es alcanzable desde fuera, esta contraseña es
           lo único que separa a cualquiera de tus claves de retransmisión. -->
      <q-banner v-if="!local" dense class="bg-orange-10 text-orange-2 rounded-borders">
        <template #avatar><q-icon :name="iAviso" color="warning" /></template>
        Este panel es accesible desde la red. Esta contraseña es lo único que protege tus
        claves de retransmisión: elige una larga y cámbiala si sospechas que se ha filtrado.
      </q-banner>

      <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
        <template #avatar><q-icon :name="iError" color="negative" /></template>
        {{ error }}
      </q-banner>
    </q-card-section>

    <q-card-actions class="q-px-md q-pb-md">
      <q-btn
        unelevated no-caps color="primary" class="full-width"
        :loading="guardando"
        :disable="!puedeSeguir"
        label="Empezar"
        @click="configurar"
      />
    </q-card-actions>
  </q-card>
</template>

<style scoped>
.asistente { width: 400px; max-width: 100%; }
.codigo :deep(input) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
</style>
