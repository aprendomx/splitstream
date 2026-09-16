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

// Validación de campos obligatorios en el cliente: solo marca error al intentar guardar,
// igual que en DialogoDestino.vue.
const nombreRef = ref(null)
const urlRef = ref(null)
const errorNombre = ref(false)
const errorUrl = ref(false)

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
  errorNombre.value = false
  errorUrl.value = false
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

/** Marca los campos obligatorios vacíos y enfoca el primero. true si todo está bien. */
function validar() {
  errorNombre.value = !nombre.value.trim()
  errorUrl.value = !url.value.trim()
  if (errorNombre.value) { nombreRef.value?.focus(); return false }
  if (errorUrl.value) { urlRef.value?.focus(); return false }
  return true
}

async function guardar() {
  if (!validar()) return
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
    <q-card class="dialogo column no-wrap" style="width: 640px; max-width: 100vw">
      <q-card-section class="row items-center no-wrap cabecera">
        <div class="col ss-t-18">{{ editando ? t('dialogo_webhook.editar_aviso') : t('dialogo_webhook.nuevo_aviso') }}</div>
        <q-btn flat round class="cerrar" :icon="iCerrar" v-close-popup size="md" :aria-label="t('comun.cerrar')" />
      </q-card-section>
      <q-card-section class="col scroll cuerpo">
        <q-input
          ref="nombreRef"
          v-model="nombre"
          :label="t('comun.nombre')"
          :error="errorNombre"
          :error-message="t('comun.campo_obligatorio')"
          outlined
          maxlength="60"
          @update:model-value="errorNombre = false"
        />
        <q-select v-model="formato" :options="formatos" emit-value map-options :label="t('dialogo_webhook.formato_label')" outlined :dropdown-icon="iDesplegar" />
        <q-input
          ref="urlRef"
          v-model="url"
          :label="t('dialogo_webhook.url_label')"
          placeholder="https://…"
          :hint="t('dialogo_webhook.hint_url')"
          :error="errorUrl"
          :error-message="t('comun.campo_obligatorio')"
          outlined
          inputmode="url"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
          @update:model-value="errorUrl = false"
        />
        <q-input v-if="formato === 'json'" v-model="secreto" :label="t('dialogo_webhook.secreto_label')" outlined
                 type="password" autocomplete="off"
                 :hint="editando && webhook?.has_secret ? t('dialogo_webhook.hint_secreto_editar') : t('dialogo_webhook.hint_secreto_nuevo')" />
        <q-select v-model="nivel" :options="niveles" emit-value map-options :label="t('dialogo_webhook.avisar_de_label')" outlined :dropdown-icon="iDesplegar" />
        <q-toggle v-model="habilitado" :label="t('dialogo_webhook.activo')" />
        <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
          <template #avatar><q-icon :name="iError" color="negative" /></template>
          {{ error }}
        </q-banner>
      </q-card-section>
      <q-card-actions class="pie">
        <q-btn flat no-caps :label="t('comun.cancelar')" @click="cerrar" />
        <q-space />
        <q-btn unelevated no-caps color="primary" :loading="guardando" :label="editando ? t('comun.guardar') : t('dialogo_webhook.crear')" @click="guardar" />
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<style scoped>
.dialogo {
  max-height: 90vh;
}
.cabecera {
  padding: var(--ss-space-4) var(--ss-space-5);
  border-bottom: 1px solid var(--ss-border);
  gap: var(--ss-space-3);
}
.cabecera .cerrar {
  min-width: 44px;
  min-height: 44px;
}
.cuerpo {
  display: flex;
  flex-direction: column;
  gap: var(--ss-space-4);
  padding: var(--ss-space-4) var(--ss-space-5);
}
.pie {
  border-top: 1px solid var(--ss-border);
  padding: var(--ss-space-3) var(--ss-space-5);
}
/* Maximizado (<600px, spec §3.4): pie apilado, el botón principal arriba. */
@media (max-width: 599.98px) {
  .pie {
    flex-direction: column-reverse;
    align-items: stretch;
    gap: var(--ss-space-2);
  }
  .pie :deep(.q-btn) {
    width: 100%;
  }
  .pie :deep(.q-space) {
    display: none;
  }
}
</style>
