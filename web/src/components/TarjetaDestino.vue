<script setup>
import { iEditar, iBorrar, iClave, iMenu, iConsejo, iArrastrar, iRotar, iProbar, iCuenta, iAlAire, iTerminar, iAbrir, iDesplegarSeccion } from '@/iconos'
import { computed, ref, watch } from 'vue'
import { porId } from '@/plataformas'
import { api } from '@/api'
import { diagnosticar, TONOS, bitrateLegible } from '@/diagnostico'
import { t, formatearNumero } from '@/i18n'
import ChipEstado from '@/components/ChipEstado.vue'

const props = defineProps({
  destino: { type: Object, required: true },
  haySesion: Boolean,
})
defineEmits(['editar', 'alternar', 'borrar', 'revelar', 'reintentar', 'probar', 'alAire', 'terminar'])

const plat = computed(() => porId(props.destino.platform))
const diag = computed(() => diagnosticar(props.destino, props.haySesion))
const tono = computed(() => TONOS[diag.value.tono])
const m = computed(() => props.destino.metrics)
const suspendido = computed(() => m.value?.state === 'suspended')
const conCifras = computed(() => m.value && props.haySesion && props.destino.enabled)
const logo = computed(() => (props.destino.logo_etag ? api.urlLogo(props.destino) : null))
const conProveedor = computed(() => Object.values(props.destino.capabilities ?? {}).some(Boolean))

// El pie «Detalles» empieza cerrado y recuerda su estado por destino: quien ya comprobó la
// URL y la clave una vez no quiere volver a abrirlo cada vez que recarga el panel.
const claveDetalles = `splitstream.detalles.${props.destino.id}`
function leerDetallesAbiertos() {
  try {
    return localStorage.getItem(claveDetalles) === '1'
  } catch {
    return false
  }
}
const detallesAbiertos = ref(leerDetallesAbiertos())
watch(detallesAbiertos, (abierto) => {
  try {
    localStorage.setItem(claveDetalles, abierto ? '1' : '0')
  } catch { /* sin localStorage, el pie vuelve a cerrado cada vez; no es grave */ }
})
const etiquetaDetalles = computed(() => t(detallesAbiertos.value ? 'destino.ocultar_detalles' : 'destino.detalles'))
</script>

<template>
  <q-card flat bordered class="tarjeta-destino column no-wrap" :class="`tono-${diag.tono}`">
    <!-- Cabecera: identidad y estado, que es lo que se lee de un vistazo. -->
    <div class="cabecera row items-center no-wrap">
      <q-icon
        :name="iArrastrar"
        size="20px"
        class="arrastre"
        tabindex="0"
        :aria-label="t('destino.reordenar', { nombre: destino.name })"
      />
      <!-- Con logo, la imagen identifica el canal y la plataforma baja a sello: se gana
           identidad sin perder de vista a qué servicio va. Sin logo, queda el icono. -->
      <div v-if="logo" class="avatar q-mr-sm">
        <img :src="logo" :alt="t('destino.alt_logo', { nombre: destino.name })" />
        <q-icon :name="plat.icono" size="12px" :style="{ color: plat.color }" class="sello" />
      </div>
      <q-icon v-else :name="plat.icono" size="22px" :style="{ color: plat.color }" class="q-mr-sm" />
      <div class="col nombre ss-t-18 ellipsis">{{ destino.name }}</div>
      <q-toggle
        :model-value="destino.enabled"
        dense
        @update:model-value="$emit('alternar')"
        :aria-label="`${destino.enabled ? t('destino.apagar') : t('destino.encender')} ${destino.name}`"
      />
      <q-btn flat round dense :icon="iMenu" size="sm" :aria-label="t('destino.mas_acciones')">
        <q-menu anchor="bottom right" self="top right">
          <q-list style="min-width: 190px">
            <q-item clickable v-close-popup @click="$emit('editar')">
              <q-item-section avatar><q-icon :name="iEditar" /></q-item-section>
              <q-item-section>{{ t('comun.editar') }}</q-item-section>
            </q-item>
            <q-item clickable v-close-popup @click="$emit('revelar')">
              <q-item-section avatar><q-icon :name="iClave" /></q-item-section>
              <q-item-section>
                {{ t('destino.ver_clave') }}
                <q-item-label caption>{{ t('destino.queda_registrado') }}</q-item-label>
              </q-item-section>
            </q-item>
            <q-item clickable v-close-popup :disable="destino.key_from_api" @click="$emit('probar')">
              <q-item-section avatar><q-icon :name="iProbar" /></q-item-section>
              <q-item-section>
                {{ t('destino.probar') }}
                <q-item-label caption>{{ destino.key_from_api ? t('destino.no_hace_falta') : t('destino.conecta_sin_emitir') }}</q-item-label>
              </q-item-section>
            </q-item>
            <q-separator />
            <q-item clickable v-close-popup class="text-negative" @click="$emit('borrar')">
              <q-item-section avatar><q-icon :name="iBorrar" /></q-item-section>
              <q-item-section>{{ t('comun.eliminar') }}</q-item-section>
            </q-item>
          </q-list>
        </q-menu>
      </q-btn>
    </div>

    <!-- Fila de estado: con icono y texto, nunca solo color. -->
    <div class="row items-center q-gutter-xs q-px-md q-pb-xs">
      <ChipEstado :tono="diag.tono" :texto="t(diag.tituloKey)" />
    </div>
    <div v-if="destino.account || conProveedor" class="row items-center q-gutter-xs q-px-md q-pb-xs">
      <span v-if="destino.account" :title="destino.account.status === 'reauth' ? t('destino.cuenta_reconectar') : t('destino.cuenta_conectada')">
        <ChipEstado
          tam="sm"
          :tono="destino.account.status === 'reauth' ? 'atencion' : 'neutro'"
          :icono="iCuenta"
          :texto="destino.account.display_name"
        />
      </span>
      <span v-else-if="conProveedor" class="ss-t-12 ss-subtle">{{ t('destino.sin_cuenta') }}</span>
    </div>

    <!-- Diagnóstico: lo que falla y qué hacer con ello, si lo hay. -->
    <div v-if="diag.detalleKey" class="ss-t-14 q-px-md q-pt-xs" :class="`text-${tono.color}`">
      {{ t(diag.detalleKey, diag.params) }}
    </div>
    <div v-if="diag.consejoKey" class="consejo ss-t-14 ss-muted q-px-md q-pt-xs">
      <q-icon :name="iConsejo" size="14px" class="q-mr-xs" />{{ t(diag.consejoKey, diag.params) }}
    </div>
    <div v-if="suspendido" class="q-px-md q-pt-sm">
      <q-btn dense no-caps unelevated color="primary" size="sm" :icon="iRotar"
             :label="t('destino.reintentar')" @click="$emit('reintentar')" />
    </div>

    <!-- Cifras: solo mientras hay sesión y el destino está encendido, o son ceros mintiendo. -->
    <div v-if="conCifras" class="cifras row q-gutter-x-md ss-t-14 ss-tabular q-px-md q-pt-sm">
      <div class="cifra column">
        <span class="ss-t-12 ss-subtle">{{ t('destino.bitrate_etiqueta') }}</span>
        <span>{{ bitrateLegible(m.bitrate_bps) }}</span>
      </div>
      <div v-if="m.dropped_frames" class="cifra column">
        <span class="ss-t-12 ss-subtle">{{ t('destino.descartes_etiqueta') }}</span>
        <span>{{ formatearNumero(m.dropped_frames) }}</span>
      </div>
      <div v-if="m.reconnections" class="cifra column">
        <span class="ss-t-12 ss-subtle">{{ t('destino.reconexiones_etiqueta') }}</span>
        <span>{{ formatearNumero(m.reconnections) }}</span>
      </div>
    </div>

    <div class="col" />

    <!-- Pie plegable: el detalle técnico importa poco salvo para copiar o depurar. -->
    <q-expansion-item
      v-model="detallesAbiertos"
      dense
      :expand-icon="iDesplegarSeccion"
      :label="etiquetaDetalles"
      header-class="pie-cabecera ss-t-14 ss-muted"
      class="pie-plegable"
    >
      <div class="ss-mono ss-t-14 ss-muted q-px-md q-pb-sm">
        <div class="ellipsis" :title="destino.rtmp_url">{{ destino.rtmp_url }}</div>
        <div class="q-mt-xs">
          <span v-if="destino.key_from_api">
            {{ t('destino.clave_api') }}
            <q-tooltip>{{ t('destino.clave_api_tooltip') }}</q-tooltip>
          </span>
          <span v-else>{{ t('destino.clave_mask', { mascara: destino.key_mask }) }}</span>
        </div>
      </div>
    </q-expansion-item>

    <!-- Acciones de emisión: fuera del plegable, siempre a la vista cuando aplican. -->
    <div v-if="destino.broadcast?.broadcast_ref" class="acciones row items-center q-gutter-sm q-pa-md">
      <q-btn
        v-if="destino.broadcast.status !== 'live'"
        unelevated no-caps color="primary" size="md"
        :icon="iAlAire" :label="t('destino.salir_al_aire')" @click="$emit('alAire')"
      />
      <q-btn
        v-else
        unelevated no-caps color="negative" size="md"
        :icon="iTerminar" :label="t('destino.terminar')" @click="$emit('terminar')"
      />
      <a
        v-if="destino.broadcast.watch_url"
        :href="destino.broadcast.watch_url"
        target="_blank"
        rel="noopener noreferrer"
        class="enlace-ver ss-t-14 ss-muted row items-center no-wrap"
      >
        <q-icon :name="iAbrir" size="14px" class="q-mr-xs" />{{ t('destino.ver_en_youtube') }}
      </a>
    </div>
  </q-card>
</template>

<style scoped>
.tarjeta-destino {
  border-left: 3px solid transparent;
  transition: border-color var(--ss-motion) var(--ss-ease);
  height: 100%;
}
.tarjeta-destino.tono-emitiendo { border-left-color: var(--ss-live); }
.tarjeta-destino.tono-atencion  { border-left-color: var(--ss-warn); }
.tarjeta-destino.tono-fallo     { border-left-color: var(--ss-danger); }
.tarjeta-destino.tono-trabajando{ border-left-color: var(--ss-info); }

.avatar {
  position: relative;
  width: 28px;
  height: 28px;
  flex: none;
}
.avatar img {
  width: 28px;
  height: 28px;
  border-radius: var(--ss-radius-sm);
  object-fit: cover;
  display: block;
  /* El logo lo elige el usuario y puede ser casi blanco o casi negro: el fondo y el borde
     tenue lo separan de la tarjeta en los dos casos. */
  background: var(--ss-surface-2);
  box-shadow: 0 0 0 1px var(--ss-border);
}
.avatar .sello {
  position: absolute;
  right: -3px;
  bottom: -3px;
  background: var(--ss-surface);
  border-radius: 50%;
  padding: 1px;
}

.cabecera {
  padding: 8px 8px 4px 4px;
  gap: 2px;
}
.nombre {
  color: var(--ss-fg);
}
.consejo { line-height: 1.45; }
.cifra { gap: 2px; }
.pie-plegable {
  border-top: 1px solid var(--ss-border);
  margin-top: var(--ss-space-2);
  background: transparent;
}
/* El encabezado del q-expansion-item es un nodo interno de Quasar: hace falta :deep()
   para que el padding llegue a alinearlo con el resto de la tarjeta. */
.pie-plegable :deep(.q-expansion-item__container .q-item) {
  padding-left: var(--ss-space-4);
  padding-right: var(--ss-space-4);
  min-height: 44px;
}
.arrastre {
  cursor: grab;
  touch-action: none;
  color: var(--ss-fg-subtle);
  /* Área táctil por encima del icono, que es pequeño a propósito: 44 px de alto. */
  padding: 12px 4px;
}
/* En puntero fino el asa se insinúa solo al pasar por la tarjeta; en táctil, sin hover
   posible, se ve siempre. */
@media (hover: hover) {
  .arrastre { opacity: 0.35; }
  .tarjeta-destino:hover .arrastre,
  .tarjeta-destino:focus-within .arrastre { opacity: 1; }
}
.enlace-ver {
  text-decoration: none;
}
.enlace-ver:hover {
  text-decoration: underline;
}
@media (prefers-reduced-motion: reduce) {
  .tarjeta-destino { transition: none; }
}
</style>
