<script setup>
import { iBorrar, iCerrar, iClaveApi, iDesplegar, iError, iInfo, iOcultar, iVer } from '@/iconos'
import { ref, computed, watch, onUnmounted } from 'vue'
import { PLATAFORMAS, porId, pideServidor, nombreDe } from '@/plataformas'
import { api, ApiError } from '@/api'
import { usePanel } from '@/stores/panel'
import ConectarCuenta from '@/components/ConectarCuenta.vue'
import { t } from '@/i18n'

// Privacidad de la emisión al crearla por API (YouTube). Por defecto «no listado»: una
// emisión creada desde el panel no se anuncia sola (spec §5). Computada, no un array
// fijo, para que las etiquetas cambien con el idioma sin recargar el componente.
const opcionesPrivacidad = computed(() => [
  { label: t('dialogo_destino.privacidad_publico'), value: 'public' },
  { label: t('dialogo_destino.privacidad_no_listado'), value: 'unlisted' },
  { label: t('dialogo_destino.privacidad_privado'), value: 'private' },
])

// Igual que arriba: las etiquetas de las capacidades de cuenta, resueltas por t() en el
// momento de pintar en vez de precalculadas.
const CAP_CLAVE = {
  title: 'dialogo_destino.cap_titulo',
  category: 'dialogo_destino.cap_categoria',
  chat: 'dialogo_destino.cap_chat',
  ingest_key: 'dialogo_destino.cap_clave_api',
  schedule: 'dialogo_destino.cap_programar',
}

const panel = usePanel()

const props = defineProps({
  modelValue: Boolean,
  // Si viene un destino, es edición; si no, alta.
  destino: { type: Object, default: null },
})
const emit = defineEmits(['update:modelValue', 'guardado'])

const editando = computed(() => Boolean(props.destino))
const paso = ref(1)
const plataforma = ref(null)
const nombre = ref('')
const servidor = ref('')
const clave = ref('')
const verClave = ref(false)
const habilitado = ref(true)
const guardando = ref(false)

// Validación de campos obligatorios en el cliente: solo marca error al intentar guardar
// (no en cada tecleo), y enfoca el primero inválido. No sustituye la validación del
// backend, que sigue siendo la fuente de verdad para todo lo demás.
const nombreRef = ref(null)
const servidorRef = ref(null)
const claveRef = ref(null)
const errorNombre = ref(false)
const errorServidor = ref(false)
const errorClave = ref(false)

/** Quita las marcas de "campo obligatorio vacío": al abrir el diálogo y al cambiar de
 * plataforma, para que un error de un intento anterior no se quede pegado a un campo
 * que la persona todavía no ha tocado. */
function limpiarErrores() {
  errorNombre.value = false
  errorServidor.value = false
  errorClave.value = false
}

// El logo elegido en el diálogo. Mientras no se guarda vive aquí, porque en el alta el
// destino todavía no tiene id al que subirlo.
const logoArchivo = ref(null)
// URL local para la vista previa. Es un blob: hay que revocarlo o se queda en memoria.
const logoPrevia = ref(null)
// Marca de "quitar el que ya tenía", distinta de "no he elegido ninguno nuevo".
const logoQuitado = ref(false)
// El error viene del backend, no de una validación duplicada aquí: la API ya devuelve
// mensajes escritos para personas y es la única fuente de verdad.
const error = ref(null)

const plat = computed(() => (plataforma.value ? porId(plataforma.value) : null))
const necesitaServidor = computed(() => plataforma.value && pideServidor(plataforma.value))
// «Otro (RTMP/RTMPS)» no tiene un menú real que describir, así que no lleva dondeKey: el
// hint se calla en vez de enseñar «Lo encuentras en: undefined».
const hintDonde = computed(() => (plat.value?.dondeKey ? t('dialogo_destino.hint_donde', { donde: t(plat.value.dondeKey) }) : ''))

// Cuenta vinculada. Solo tiene sentido para plataformas con proveedor propio (Twitch, y
// las que se sumen): custom, TikTok, X y Facebook no llevan bloque de cuenta.
const cuentas = ref([])
const cuentaId = ref(null)
const plataformaConProveedor = computed(
  () => panel.plataformas.find((p) => p.id === plataforma.value)?.configured !== undefined,
)
const plataformaConfigurada = computed(
  () => panel.plataformas.find((p) => p.id === plataforma.value)?.configured === true,
)
const capacidades = computed(
  () => panel.plataformas.find((p) => p.id === plataforma.value)?.capabilities ?? null,
)

// Clave por API (YouTube, Kick): la plataforma da la URL y la clave, no se pegan a mano.
const usaClaveAPI = computed(() => Boolean(capacidades.value?.ingest_key))
// En el alta, un enlace deja volver al formulario de siempre; en edición no aplica, porque
// ahí la clave a mano sigue siendo el campo «Clave nueva» de toda la vida.
const claveManual = ref(false)
const mostrarBloqueAPI = computed(() => usaClaveAPI.value && !editando.value && !claveManual.value)

const tituloEmision = ref('')
const privacidadEmision = ref('unlisted')
const horaEmision = ref('') // datetime-local; vacío = ahora
const creandoEmision = ref(false)
const errorEmision = ref(null)

/** ISO 8601 con zona, o undefined si no se puso hora: el backend trata la ausencia como «ahora». */
function horaEmisionISO() {
  return horaEmision.value ? new Date(horaEmision.value).toISOString() : undefined
}

/** Alta: crea el destino a partir de la cuenta, con la emisión (YouTube) o la clave (Kick). */
async function crearConCuenta() {
  if (!cuentaId.value) return
  creandoEmision.value = true
  errorEmision.value = null
  try {
    const body = { account_id: cuentaId.value, name: nombre.value }
    if (plataforma.value === 'youtube') {
      if (tituloEmision.value.trim()) body.title = tituloEmision.value.trim()
      body.privacy = privacidadEmision.value
      const iso = horaEmisionISO()
      if (iso) body.scheduled_at = iso
    }
    await api.crearDestinoDesdeCuenta(body)
    emit('guardado', null)
    cerrar()
  } catch (e) {
    errorEmision.value = e instanceof ApiError ? e.message : t('dialogo_destino.error_traer_clave')
  } finally {
    creandoEmision.value = false
  }
}

/** Edición: releer la clave o abrir una emisión nueva sobre el destino ya existente. */
async function nuevaEmision() {
  creandoEmision.value = true
  errorEmision.value = null
  try {
    const body = {}
    if (plataforma.value === 'youtube') {
      if (tituloEmision.value.trim()) body.title = tituloEmision.value.trim()
      if (privacidadEmision.value) body.privacy = privacidadEmision.value
      const iso = horaEmisionISO()
      if (iso) body.scheduled_at = iso
    }
    await api.crearEmision(props.destino.id, body)
    emit('guardado', null)
    cerrar()
  } catch (e) {
    errorEmision.value = e instanceof ApiError ? e.message : t('dialogo_destino.error_releer_clave')
  } finally {
    creandoEmision.value = false
  }
}

async function cargarCuentas() {
  if (!plataforma.value) { cuentas.value = []; return }
  try {
    const todas = await api.cuentas()
    cuentas.value = todas.filter((c) => c.platform === plataforma.value)
  } catch {
    cuentas.value = []
  }
}

watch(
  () => props.modelValue,
  (abierto) => {
    if (!abierto) return
    error.value = null
    guardando.value = false
    verClave.value = false
    limpiarErrores()
    soltarPrevia()
    logoArchivo.value = null
    logoQuitado.value = false
    claveManual.value = false
    tituloEmision.value = ''
    privacidadEmision.value = 'unlisted'
    horaEmision.value = ''
    errorEmision.value = null
    creandoEmision.value = false
    if (props.destino) {
      plataforma.value = props.destino.platform
      nombre.value = props.destino.name
      servidor.value = props.destino.rtmp_url
      habilitado.value = props.destino.enabled
      clave.value = ''
      cuentaId.value = props.destino.account?.id ?? null
      paso.value = 2
    } else {
      plataforma.value = null
      nombre.value = ''
      servidor.value = ''
      clave.value = ''
      habilitado.value = true
      cuentaId.value = null
      paso.value = 1
    }
    cargarCuentas()
  },
)

/** Tras vincular desde el diálogo: refrescar la lista y dejar la cuenta nueva elegida. */
async function trasConectar(cuenta) {
  await cargarCuentas()
  cuentaId.value = cuenta.id
}

function soltarPrevia() {
  if (logoPrevia.value) {
    URL.revokeObjectURL(logoPrevia.value)
    logoPrevia.value = null
  }
}

onUnmounted(soltarPrevia)

function elegirLogo(archivo) {
  soltarPrevia()
  logoArchivo.value = archivo ?? null
  logoQuitado.value = false
  if (archivo) logoPrevia.value = URL.createObjectURL(archivo)
}

function quitarLogo() {
  soltarPrevia()
  logoArchivo.value = null
  logoQuitado.value = true
}

// Lo que se enseña: la previa de lo recién elegido, o el logo que ya tenía el destino.
const logoVisible = computed(() => {
  if (logoPrevia.value) return logoPrevia.value
  if (logoQuitado.value) return null
  if (props.destino?.logo_etag) return api.urlLogo(props.destino)
  return null
})

function elegir(p) {
  plataforma.value = p.id
  // El nombre se propone, no se impone: es lo que el usuario verá en la lista.
  if (!nombre.value) nombre.value = nombreDe(p)
  if (p.url) servidor.value = p.url
  cuentaId.value = null
  // Un intento de guardar fallido en la plataforma anterior no debe dejar campos
  // marcados en rojo en esta, que la persona ni ha visto todavía.
  limpiarErrores()
  cargarCuentas()
  paso.value = 2
}

function cerrar() {
  emit('update:modelValue', false)
}

/** Marca los campos obligatorios vacíos y enfoca el primero. true si todo está bien. */
function validar() {
  errorNombre.value = !nombre.value.trim()
  errorServidor.value = necesitaServidor.value && !servidor.value.trim()
  // Al crear, la clave hace falta ya (el alta la manda en el POST); al editar, vacía
  // significa "no la toques".
  errorClave.value = !editando.value && !clave.value.trim()
  if (errorNombre.value) { nombreRef.value?.focus(); return false }
  if (errorServidor.value) { servidorRef.value?.focus(); return false }
  if (errorClave.value) { claveRef.value?.focus(); return false }
  return true
}

async function guardar() {
  if (!validar()) return
  guardando.value = true
  error.value = null
  try {
    let id
    if (editando.value) {
      const patch = {
        name: nombre.value,
        platform: plataforma.value,
        rtmp_url: servidor.value,
        enabled: habilitado.value,
      }
      // Clave vacía significa "no la toques". Mandarla vacía la borraría, y el backend la
      // rechazaría con un error sobre un campo que el usuario ni tocó.
      if (clave.value) patch.key = clave.value
      // account_id solo tiene sentido si la plataforma tiene proveedor; null desvincula.
      if (plataformaConProveedor.value) patch.account_id = cuentaId.value
      await api.editarDestino(props.destino.id, patch)
      id = props.destino.id
    } else {
      const creado = await api.crearDestino({
        name: nombre.value,
        platform: plataforma.value,
        rtmp_url: servidor.value,
        key: clave.value,
        enabled: habilitado.value,
      })
      id = creado.id
    }

    // A partir de aquí el destino YA EXISTE. Lo que falle después (la cuenta en el alta,
    // el logo) se avisa y el diálogo se cierra igual: dejarlo abierto con un error
    // genérico invita a pulsar «Vincular» otra vez y crear el canal por duplicado.
    let avisoLogo = null
    // El destino todavía no existía cuando se eligió la cuenta: vincular es un PATCH
    // aparte, tras crear.
    if (!editando.value && plataformaConProveedor.value && cuentaId.value !== null) {
      try {
        await api.editarDestino(id, { account_id: cuentaId.value })
      } catch (e) {
        avisoLogo = e instanceof ApiError ? `${t('dialogo_destino.error_vincular_cuenta')}: ${e.message}` : t('dialogo_destino.error_vincular_cuenta')
      }
    }
    try {
      if (logoArchivo.value) await api.subirLogo(id, logoArchivo.value)
      else if (logoQuitado.value) await api.quitarLogo(id)
    } catch (e) {
      const motivo = e instanceof ApiError ? e.message : t('dialogo_destino.error_guardar_logo')
      avisoLogo = avisoLogo ? `${avisoLogo}; ${motivo}` : motivo
    }

    emit('guardado', avisoLogo)
    cerrar()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : t('comun.no_se_pudo_guardar')
  } finally {
    guardando.value = false
  }
}
</script>

<template>
  <q-dialog
    :model-value="modelValue"
    @update:model-value="emit('update:modelValue', $event)"
    :maximized="$q.screen.lt.sm"
    transition-show="jump-up"
    transition-hide="jump-down"
  >
    <q-card class="dialogo column no-wrap" style="width: 640px; max-width: 100vw">
      <q-card-section class="row items-center no-wrap cabecera">
        <div class="col">
          <div class="ss-t-18">
            {{ editando ? t('dialogo_destino.editar_destino') : t('dialogo_destino.vincular_canal') }}
          </div>
          <div class="ss-t-12 ss-subtle">{{ t('dialogo_destino.paso_de', { n: paso }) }}</div>
        </div>
        <q-btn flat round class="cerrar" :icon="iCerrar" v-close-popup size="md" :aria-label="t('comun.cerrar')" />
      </q-card-section>

      <!-- Paso 1: elegir plataforma. Precargar la URL evita la clase entera de error
           "URL mal escrita", que el usuario no debería llegar a ver nunca. -->
      <q-card-section v-if="paso === 1" class="col scroll cuerpo">
        <p class="ss-t-14 ss-muted">
          {{ t('dialogo_destino.elige_plataforma') }}
        </p>
        <div class="rejilla-plataformas">
          <button
            v-for="p in PLATAFORMAS"
            :key="p.id"
            class="tarjeta-plataforma"
            :class="{ seleccionada: plataforma === p.id }"
            type="button"
            :aria-pressed="plataforma === p.id"
            @click="elegir(p)"
          >
            <q-icon :name="p.icono" size="32px" :style="{ color: p.color }" />
            <span class="nombre ss-t-14">{{ nombreDe(p) }}</span>
          </button>
        </div>
      </q-card-section>

      <!-- Paso 2: los datos. -->
      <q-card-section v-else class="col scroll cuerpo">
        <div v-if="plat" class="row items-center q-gutter-sm cabecera-plataforma">
          <q-icon :name="plat.icono" size="24px" :style="{ color: plat.color }" />
          <div class="ss-t-16">{{ nombreDe(plat) }}</div>
          <q-space />
          <q-btn
            v-if="!editando"
            flat
            dense
            no-caps
            size="sm"
            :label="t('dialogo_destino.cambiar')"
            @click="paso = 1"
          />
        </div>

        <q-banner v-if="plat?.notaKey" dense class="bg-grey-9 ss-muted rounded-borders">
          <template #avatar><q-icon :name="iInfo" color="info" /></template>
          {{ t(plat.notaKey) }}
        </q-banner>

        <q-input
          ref="nombreRef"
          v-model="nombre"
          :label="t('comun.nombre')"
          :hint="t('dialogo_destino.hint_nombre')"
          :error="errorNombre"
          :error-message="t('comun.campo_obligatorio')"
          outlined
          maxlength="60"
          @update:model-value="errorNombre = false"
        />

        <div class="row items-center q-gutter-md bloque-logo">
          <div class="previa-logo">
            <img v-if="logoVisible" :src="logoVisible" :alt="t('dialogo_destino.alt_logo')" />
            <q-icon v-else :name="plat?.icono" size="24px" :style="{ color: plat?.color }" />
          </div>
          <div class="col column q-gutter-xs">
            <q-file
              :model-value="logoArchivo"
              @update:model-value="elegirLogo"
              :label="t('dialogo_destino.logo_label')"
              :hint="t('dialogo_destino.hint_logo')"
              accept="image/png,image/jpeg"
              outlined
              clearable
              :clear-icon="iCerrar"
              @clear="elegirLogo(null)"
            />
          </div>
          <q-btn
            v-if="logoVisible"
            flat
            dense
            round
            :icon="iBorrar"
            :aria-label="t('dialogo_destino.quitar_logo')"
            @click="quitarLogo"
          />
        </div>

        <div v-if="plataformaConProveedor" class="bloque-cuenta q-gutter-y-sm">
          <p class="ss-t-14 ss-muted">{{ t('dialogo_destino.cuenta_caption') }}</p>
          <div class="row items-center q-gutter-xs">
            <q-chip v-for="c in ['title','category','chat','ingest_key','schedule']" :key="c" dense square size="sm"
                    :color="capacidades?.[c] ? 'primary' : 'grey-8'" :text-color="capacidades?.[c] ? 'white' : 'grey-5'">
              {{ t(CAP_CLAVE[c]) }}
            </q-chip>
          </div>
          <q-select v-if="cuentas.length" v-model="cuentaId" :options="[{label: t('dialogo_destino.sin_cuenta_opcion'), value: null}, ...cuentas.map(c => ({label: c.display_name + (c.status === 'reauth' ? t('dialogo_destino.reconectar_sufijo') : ''), value: c.id}))]"
                    emit-value map-options outlined :label="t('dialogo_destino.cuenta_vinculada_label')" :dropdown-icon="iDesplegar" />
          <ConectarCuenta v-if="plataformaConfigurada" :plataforma="plataforma" :nombre="nombreDe(plat)"
                          :requiere-app="Boolean(capacidades?.requires_own_app)" @conectada="trasConectar" />
          <q-banner v-else dense class="bg-grey-9 ss-muted rounded-borders">
            {{ t('dialogo_destino.banner_necesita_client_id_pre', { plataforma: nombreDe(plat) }) }} <code>SPLITSTREAM_TWITCH_CLIENT_ID</code> {{ t('dialogo_destino.banner_necesita_client_id_post') }}
          </q-banner>
        </div>

        <!-- Clave por API en el alta: la plataforma da la URL y la clave, no se pegan a
             mano. Sustituye por completo a servidor + clave mientras no se pida lo
             contrario. -->
        <div v-if="mostrarBloqueAPI" class="bloque-clave-api q-gutter-y-sm">
          <p class="ss-t-14 ss-muted">{{ t('dialogo_destino.clave_api_caption') }}</p>
          <template v-if="plataforma === 'youtube'">
            <q-input v-model="tituloEmision" :label="t('dialogo_destino.titulo_emision_label')" :placeholder="nombre" outlined maxlength="140" />
            <q-select v-model="privacidadEmision" :options="opcionesPrivacidad" emit-value map-options outlined :label="t('dialogo_destino.privacidad_label')" :dropdown-icon="iDesplegar" />
            <q-input v-model="horaEmision" type="datetime-local" outlined :label="t('dialogo_destino.hora_label')" :hint="t('dialogo_destino.hora_hint')" />
          </template>
          <p v-if="!cuentaId" class="ss-t-14 ss-muted">{{ t('dialogo_destino.elige_cuenta_arriba') }}</p>
          <q-btn
            unelevated
            no-caps
            color="primary"
            :icon="iClaveApi"
            :loading="creandoEmision"
            :disable="!cuentaId"
            :label="plataforma === 'youtube' ? t('dialogo_destino.crear_emision_youtube') : t('dialogo_destino.traer_clave_kick')"
            @click="crearConCuenta"
          />
          <div>
            <q-btn flat dense no-caps color="primary" class="ss-t-14 enlace" :label="t('dialogo_destino.pegar_clave_mano')" @click="claveManual = true" />
          </div>
          <q-banner v-if="errorEmision" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">{{ errorEmision }}</q-banner>
        </div>

        <template v-else>
          <q-input
            v-if="necesitaServidor"
            ref="servidorRef"
            v-model="servidor"
            :label="t('dialogo_destino.servidor_label')"
            placeholder="rtmp://…"
            :hint="hintDonde"
            :error="errorServidor"
            :error-message="t('comun.campo_obligatorio')"
            outlined
            inputmode="url"
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
            @update:model-value="errorServidor = false"
          />
          <div v-else class="servidor-fijo">
            <div class="etiqueta ss-t-12 ss-muted">{{ t('dialogo_destino.servidor_label') }}</div>
            <div class="valor ss-t-14 ss-mono ss-muted">{{ servidor }}</div>
          </div>

          <q-input
            ref="claveRef"
            v-model="clave"
            :label="editando ? t('dialogo_destino.clave_nueva_label') : t('dialogo_destino.clave_retransmision_label')"
            :type="verClave ? 'text' : 'password'"
            :hint="
              editando
                ? t('dialogo_destino.hint_clave_actual', { mascara: destino.key_mask })
                : hintDonde
            "
            :error="errorClave"
            :error-message="t('comun.campo_obligatorio')"
            outlined
            autocapitalize="off"
            autocorrect="off"
            spellcheck="false"
            autocomplete="off"
            @update:model-value="errorClave = false"
          >
            <template #append>
              <q-btn
                flat
                round
                dense
                :icon="verClave ? iOcultar : iVer"
                :aria-label="verClave ? t('dialogo_destino.ocultar_clave') : t('dialogo_destino.mostrar_clave')"
                @click="verClave = !verClave"
              />
            </template>
          </q-input>

          <!-- Alta: si esta plataforma da clave por API, un enlace vuelve al bloque de
               arriba en vez de pegarla a mano. -->
          <div v-if="usaClaveAPI && !editando">
            <q-btn flat dense no-caps color="primary" class="ss-t-14 enlace" :label="t('dialogo_destino.traer_clave_api')" @click="claveManual = false" />
          </div>

          <!-- Edición: sobre un destino con cuenta y clave por API, releer la clave o abrir
               una emisión nueva sin tocar el resto del formulario. -->
          <div v-if="usaClaveAPI && editando" class="bloque-clave-api q-gutter-y-sm">
            <template v-if="plataforma === 'youtube'">
              <q-input v-model="tituloEmision" :label="t('dialogo_destino.titulo_nueva_emision_label')" :placeholder="nombre" outlined maxlength="140" />
              <q-select v-model="privacidadEmision" :options="opcionesPrivacidad" emit-value map-options outlined :label="t('dialogo_destino.privacidad_label')" :dropdown-icon="iDesplegar" />
              <q-input v-model="horaEmision" type="datetime-local" outlined :label="t('dialogo_destino.hora_label')" :hint="t('dialogo_destino.hora_hint')" />
            </template>
            <q-btn
              flat
              no-caps
              color="primary"
              :icon="iClaveApi"
              :loading="creandoEmision"
              :label="t('dialogo_destino.nueva_emision_releer')"
              @click="nuevaEmision"
            />
            <q-banner v-if="errorEmision" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">{{ errorEmision }}</q-banner>
          </div>
        </template>

        <q-toggle v-model="habilitado" :label="t('dialogo_destino.retransmitir_toggle')" />

        <!-- El error del backend, junto al formulario y anunciado a lectores de pantalla. -->
        <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
          <template #avatar><q-icon :name="iError" color="negative" /></template>
          {{ error }}
        </q-banner>
      </q-card-section>

      <q-card-actions v-if="paso === 2" class="pie">
        <q-btn flat no-caps :label="t('comun.cancelar')" @click="cerrar" />
        <q-space />
        <!-- Con la clave por API, la petición sale del botón «Crear emisión…» de arriba:
             este cierra el diálogo por su cuenta, así que aquí no hace falta un «Vincular»
             que intentaría guardar sin clave. -->
        <q-btn
          v-if="!mostrarBloqueAPI"
          unelevated
          no-caps
          color="primary"
          :loading="guardando"
          :label="editando ? t('comun.guardar') : t('dialogo_destino.vincular')"
          @click="guardar"
        />
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
/* El botón cerrar cubre el objetivo táctil de 44px aunque el tamaño visual de Quasar
   ("md") se quede un poco corto. */
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
/* Enlaces secundarios convertidos en q-btn: el hueco visual es "dense", pero el
   objetivo táctil se mantiene en 44px. */
.enlace {
  min-height: 44px;
}
.rejilla-plataformas {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  gap: var(--ss-space-3);
}
.tarjeta-plataforma {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--ss-space-2);
  min-height: 96px;
  padding: var(--ss-space-3) var(--ss-space-2);
  border: 1px solid var(--ss-border);
  border-radius: var(--ss-radius-md);
  background: var(--ss-surface);
  color: inherit;
  font: inherit;
  cursor: pointer;
  transition: background-color var(--ss-motion) var(--ss-ease), border-color var(--ss-motion) var(--ss-ease);
}
.tarjeta-plataforma:hover {
  border-color: var(--ss-border-strong);
}
.tarjeta-plataforma.seleccionada {
  border-color: var(--ss-primary);
  background: color-mix(in srgb, var(--ss-primary) 12%, var(--ss-surface));
}
.tarjeta-plataforma .nombre {
  text-align: center;
  line-height: 1.2;
}
.previa-logo {
  width: 48px;
  height: 48px;
  flex: none;
  border-radius: var(--ss-radius-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--ss-surface);
  box-shadow: 0 0 0 1px var(--ss-border);
  overflow: hidden;
}
.previa-logo img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.bloque-logo {
  flex-wrap: nowrap;
}
.cabecera-plataforma {
  padding-bottom: var(--ss-space-1);
  border-bottom: 1px solid var(--ss-border);
}
.servidor-fijo .etiqueta {
  margin-bottom: 2px;
}
.servidor-fijo .valor {
  word-break: break-all;
}
@media (prefers-reduced-motion: reduce) {
  .tarjeta-plataforma { transition: none; }
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
