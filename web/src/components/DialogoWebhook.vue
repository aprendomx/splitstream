<script setup>
import { ref, computed, watch } from 'vue'
import { iCerrar, iError } from '@/iconos'
import { api, ApiError } from '@/api'

const props = defineProps({ modelValue: Boolean, webhook: { type: Object, default: null } })
const emit = defineEmits(['update:modelValue', 'guardado'])

const editando = computed(() => Boolean(props.webhook))
const nombre = ref('')
const url = ref('')
const formato = ref('discord')
const secreto = ref('')
const nivel = ref('warn')
const habilitado = ref(true)
const guardando = ref(false)
const error = ref(null)

const FORMATOS = [
  { value: 'discord', label: 'Discord' },
  { value: 'slack', label: 'Slack' },
  { value: 'json', label: 'JSON genérico (con firma)' },
]
const NIVELES = [
  { value: 'error', label: 'Solo errores' },
  { value: 'warn', label: 'Avisos y errores' },
  { value: 'info', label: 'Todo' },
]

watch(() => props.modelValue, (abierto) => {
  if (!abierto) return
  error.value = null
  guardando.value = false
  secreto.value = ''
  if (props.webhook) {
    nombre.value = props.webhook.name
    url.value = props.webhook.url
    formato.value = props.webhook.format
    nivel.value = props.webhook.min_level
    habilitado.value = props.webhook.enabled
  } else {
    nombre.value = ''
    url.value = ''
    formato.value = 'discord'
    nivel.value = 'warn'
    habilitado.value = true
  }
})

function cerrar() { emit('update:modelValue', false) }

async function guardar() {
  guardando.value = true
  error.value = null
  try {
    const datos = { name: nombre.value, url: url.value, format: formato.value, min_level: nivel.value, enabled: habilitado.value }
    if (editando.value) {
      // Secreto vacío al editar = "no lo toques". Quitar un secreto existente no tiene
      // botón todavía: se hace con un PATCH con secret vacío.
      if (secreto.value) datos.secret = secreto.value
      await api.editarWebhook(props.webhook.id, datos)
    } else {
      datos.secret = secreto.value
      await api.crearWebhook(datos)
    }
    emit('guardado')
    cerrar()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'No se pudo guardar'
  } finally {
    guardando.value = false
  }
}
</script>

<template>
  <q-dialog :model-value="modelValue" @update:model-value="emit('update:modelValue', $event)"
            :maximized="$q.screen.lt.sm">
    <q-card class="dialogo-webhook column no-wrap">
      <q-card-section class="row items-center q-pb-sm">
        <div class="text-h6">{{ editando ? 'Editar aviso' : 'Nuevo aviso' }}</div>
        <q-space />
        <q-btn flat round dense :icon="iCerrar" aria-label="Cerrar" @click="cerrar" />
      </q-card-section>
      <q-card-section class="col scroll q-pt-none q-gutter-y-md">
        <q-input v-model="nombre" label="Nombre" outlined dense maxlength="60" />
        <q-select v-model="formato" :options="FORMATOS" emit-value map-options label="Formato" outlined dense />
        <q-input v-model="url" label="URL" placeholder="https://…" outlined dense inputmode="url"
                 autocapitalize="off" autocorrect="off" spellcheck="false"
                 hint="Discord y Slack te dan la URL al crear el webhook en el canal. Solo https." />
        <q-input v-if="formato === 'json'" v-model="secreto" label="Secreto para firmar" outlined dense
                 type="password" autocomplete="off"
                 :hint="editando && webhook?.has_secret ? 'Déjalo vacío para conservar el actual' : 'Opcional. Se manda como HMAC-SHA256 en X-Splitstream-Signature'" />
        <q-select v-model="nivel" :options="NIVELES" emit-value map-options label="Avisar de" outlined dense />
        <q-toggle v-model="habilitado" label="Activo" />
        <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
          <template #avatar><q-icon :name="iError" color="negative" /></template>
          {{ error }}
        </q-banner>
      </q-card-section>
      <q-card-actions align="right" class="q-pa-md">
        <q-btn flat no-caps label="Cancelar" @click="cerrar" />
        <q-btn unelevated no-caps color="primary" :loading="guardando" :label="editando ? 'Guardar' : 'Crear'" @click="guardar" />
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<style scoped>
.dialogo-webhook { width: 480px; max-width: 100vw; max-height: 90vh; }
</style>
