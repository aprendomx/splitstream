<script setup>
import { ref, computed, watch } from 'vue'
import { iCerrar, iDesplegar, iError } from '@/iconos'
import { api, ApiError } from '@/api'
import { t } from '@/i18n'

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

// Computadas, no arrays fijos: «Discord»/«Slack» son nombres propios y no se traducen,
// pero la etiqueta de JSON sí, y debe reaccionar al cambio de idioma.
const formatos = computed(() => [
  { value: 'discord', label: 'Discord' },
  { value: 'slack', label: 'Slack' },
  { value: 'json', label: t('dialogo_webhook.formato_json') },
])
const niveles = computed(() => [
  { value: 'error', label: t('dialogo_webhook.nivel_error') },
  { value: 'warn', label: t('dialogo_webhook.nivel_warn') },
  { value: 'info', label: t('dialogo_webhook.nivel_info') },
])

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
    error.value = e instanceof ApiError ? e.message : t('comun.no_se_pudo_guardar')
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
        <div class="text-h6">{{ editando ? t('dialogo_webhook.editar_aviso') : t('dialogo_webhook.nuevo_aviso') }}</div>
        <q-space />
        <q-btn flat round dense :icon="iCerrar" :aria-label="t('comun.cerrar')" @click="cerrar" />
      </q-card-section>
      <q-card-section class="col scroll q-pt-none q-gutter-y-md">
        <q-input v-model="nombre" :label="t('comun.nombre')" outlined dense maxlength="60" />
        <q-select v-model="formato" :options="formatos" emit-value map-options :label="t('dialogo_webhook.formato_label')" outlined dense :dropdown-icon="iDesplegar" />
        <q-input v-model="url" :label="t('dialogo_webhook.url_label')" placeholder="https://…" outlined dense inputmode="url"
                 autocapitalize="off" autocorrect="off" spellcheck="false"
                 :hint="t('dialogo_webhook.hint_url')" />
        <q-input v-if="formato === 'json'" v-model="secreto" :label="t('dialogo_webhook.secreto_label')" outlined dense
                 type="password" autocomplete="off"
                 :hint="editando && webhook?.has_secret ? t('dialogo_webhook.hint_secreto_editar') : t('dialogo_webhook.hint_secreto_nuevo')" />
        <q-select v-model="nivel" :options="niveles" emit-value map-options :label="t('dialogo_webhook.avisar_de_label')" outlined dense :dropdown-icon="iDesplegar" />
        <q-toggle v-model="habilitado" :label="t('dialogo_webhook.activo')" />
        <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
          <template #avatar><q-icon :name="iError" color="negative" /></template>
          {{ error }}
        </q-banner>
      </q-card-section>
      <q-card-actions align="right" class="q-pa-md">
        <q-btn flat no-caps :label="t('comun.cancelar')" @click="cerrar" />
        <q-btn unelevated no-caps color="primary" :loading="guardando" :label="editando ? t('comun.guardar') : t('dialogo_webhook.crear')" @click="guardar" />
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<style scoped>
.dialogo-webhook { width: 480px; max-width: 100vw; max-height: 90vh; }
</style>
