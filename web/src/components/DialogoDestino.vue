<script setup>
import { iBorrar, iCerrar, iError, iInfo, iOcultar, iVer } from '@/iconos'
import { ref, computed, watch, onUnmounted } from 'vue'
import { PLATAFORMAS, porId, pideServidor } from '@/plataformas'
import { api, ApiError } from '@/api'

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

watch(
  () => props.modelValue,
  (abierto) => {
    if (!abierto) return
    error.value = null
    guardando.value = false
    verClave.value = false
    soltarPrevia()
    logoArchivo.value = null
    logoQuitado.value = false
    if (props.destino) {
      plataforma.value = props.destino.platform
      nombre.value = props.destino.name
      servidor.value = props.destino.rtmp_url
      habilitado.value = props.destino.enabled
      clave.value = ''
      paso.value = 2
    } else {
      plataforma.value = null
      nombre.value = ''
      servidor.value = ''
      clave.value = ''
      habilitado.value = true
      paso.value = 1
    }
  },
)

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
  if (!nombre.value) nombre.value = p.nombre
  if (p.url) servidor.value = p.url
  paso.value = 2
}

function cerrar() {
  emit('update:modelValue', false)
}

async function guardar() {
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

    // El logo va en una petición aparte porque en el alta el destino no tenía id hasta
    // ahora mismo. Si falla, el canal QUEDA CREADO y se avisa: deshacer la creación a
    // espaldas del usuario sería peor que un logo que falta.
    let avisoLogo = null
    try {
      if (logoArchivo.value) await api.subirLogo(id, logoArchivo.value)
      else if (logoQuitado.value) await api.quitarLogo(id)
    } catch (e) {
      avisoLogo = e instanceof ApiError ? e.message : 'No se pudo guardar el logo'
    }

    emit('guardado', avisoLogo)
    cerrar()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'No se pudo guardar'
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
    <q-card class="dialogo-destino column no-wrap">
      <q-card-section class="row items-center q-pb-sm">
        <div class="text-h6">
          {{ editando ? 'Editar destino' : 'Vincular un canal' }}
        </div>
        <q-space />
        <q-btn flat round dense :icon="iCerrar" aria-label="Cerrar" @click="cerrar" />
      </q-card-section>

      <!-- Paso 1: elegir plataforma. Precargar la URL evita la clase entera de error
           "URL mal escrita", que el usuario no debería llegar a ver nunca. -->
      <q-card-section v-if="paso === 1" class="col scroll q-pt-none">
        <p class="text-body2 text-grey-5 q-mb-md">
          Elige dónde quieres retransmitir. Cargaremos su servidor por ti.
        </p>
        <div class="rejilla-plataformas">
          <button
            v-for="p in PLATAFORMAS"
            :key="p.id"
            class="tarjeta-plataforma"
            type="button"
            @click="elegir(p)"
          >
            <q-icon :name="p.icono" size="28px" :style="{ color: p.color }" />
            <span class="nombre">{{ p.nombre }}</span>
          </button>
        </div>
      </q-card-section>

      <!-- Paso 2: los datos. -->
      <q-card-section v-else class="col scroll q-pt-none q-gutter-y-md">
        <div v-if="plat" class="row items-center q-gutter-sm cabecera-plataforma">
          <q-icon :name="plat.icono" size="24px" :style="{ color: plat.color }" />
          <div class="text-subtitle1">{{ plat.nombre }}</div>
          <q-space />
          <q-btn
            v-if="!editando"
            flat
            dense
            no-caps
            size="sm"
            label="Cambiar"
            @click="paso = 1"
          />
        </div>

        <q-banner v-if="plat?.nota" dense class="bg-grey-9 text-grey-3 rounded-borders">
          <template #avatar><q-icon :name="iInfo" color="info" /></template>
          {{ plat.nota }}
        </q-banner>

        <q-input
          v-model="nombre"
          label="Nombre"
          hint="Solo para que lo reconozcas en la lista"
          outlined
          dense
          maxlength="60"
        />

        <div class="row items-center q-gutter-md bloque-logo">
          <div class="previa-logo">
            <img v-if="logoVisible" :src="logoVisible" alt="Vista previa del logo" />
            <q-icon v-else :name="plat?.icono" size="24px" :style="{ color: plat?.color }" />
          </div>
          <div class="col column q-gutter-xs">
            <q-file
              :model-value="logoArchivo"
              @update:model-value="elegirLogo"
              label="Logo"
              hint="PNG o JPEG. Opcional."
              accept="image/png,image/jpeg"
              outlined
              dense
              clearable
              @clear="elegirLogo(null)"
            />
          </div>
          <q-btn
            v-if="logoVisible"
            flat
            dense
            round
            :icon="iBorrar"
            aria-label="Quitar el logo"
            @click="quitarLogo"
          />
        </div>

        <q-input
          v-if="necesitaServidor"
          v-model="servidor"
          label="Servidor"
          placeholder="rtmp://…"
          :hint="plat ? `Lo encuentras en: ${plat.donde}` : ''"
          outlined
          dense
          inputmode="url"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
        />
        <div v-else class="servidor-fijo">
          <div class="etiqueta">Servidor</div>
          <div class="valor">{{ servidor }}</div>
        </div>

        <q-input
          v-model="clave"
          :label="editando ? 'Clave nueva' : 'Clave de retransmisión'"
          :type="verClave ? 'text' : 'password'"
          :hint="
            editando
              ? `Déjala vacía para conservar la actual (${destino.key_mask})`
              : plat
                ? `La encuentras en: ${plat.donde}`
                : ''
          "
          outlined
          dense
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
          autocomplete="off"
        >
          <template #append>
            <q-btn
              flat
              round
              dense
              :icon="verClave ? iOcultar : iVer"
              :aria-label="verClave ? 'Ocultar la clave' : 'Mostrar la clave'"
              @click="verClave = !verClave"
            />
          </template>
        </q-input>

        <q-toggle v-model="habilitado" label="Retransmitir a este destino" />

        <!-- El error del backend, junto al formulario y anunciado a lectores de pantalla. -->
        <q-banner v-if="error" dense class="bg-red-10 text-red-2 rounded-borders" role="alert">
          <template #avatar><q-icon :name="iError" color="negative" /></template>
          {{ error }}
        </q-banner>
      </q-card-section>

      <q-card-actions v-if="paso === 2" align="right" class="q-pa-md">
        <q-btn flat no-caps label="Cancelar" @click="cerrar" />
        <q-btn
          unelevated
          no-caps
          color="primary"
          :loading="guardando"
          :label="editando ? 'Guardar' : 'Vincular'"
          @click="guardar"
        />
      </q-card-actions>
    </q-card>
  </q-dialog>
</template>

<style scoped>
.dialogo-destino {
  width: 480px;
  max-width: 100vw;
  max-height: 90vh;
}
.rejilla-plataformas {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
  gap: 10px;
}
.tarjeta-plataforma {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  /* Muy por encima del mínimo táctil de 44px: se pulsa con el pulgar y a veces con prisa. */
  min-height: 88px;
  padding: 12px 8px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.03);
  color: inherit;
  font: inherit;
  cursor: pointer;
  transition: background-color 160ms ease, border-color 160ms ease;
}
.tarjeta-plataforma:hover {
  background: rgba(255, 255, 255, 0.07);
  border-color: rgba(255, 255, 255, 0.24);
}
.tarjeta-plataforma:focus-visible {
  outline: 2px solid var(--q-primary);
  outline-offset: 2px;
}
.previa-logo {
  width: 48px;
  height: 48px;
  flex: none;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(255, 255, 255, 0.06);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.12);
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

.tarjeta-plataforma .nombre {
  font-size: 13px;
  text-align: center;
  line-height: 1.2;
}
.cabecera-plataforma {
  padding-bottom: 4px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}
.servidor-fijo .etiqueta {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.55);
  margin-bottom: 2px;
}
.servidor-fijo .valor {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  color: rgba(255, 255, 255, 0.75);
  word-break: break-all;
}
@media (prefers-reduced-motion: reduce) {
  .tarjeta-plataforma { transition: none; }
}
</style>
