<script setup>
import { iEditar, iBorrar, iClave, iMenu, iConsejo, iArrastrar, iRotar, iProbar, iCuenta, iAlAire, iTerminar, iAbrir } from '@/iconos'
import { computed } from 'vue'
import { porId } from '@/plataformas'
import { api } from '@/api'
import { diagnosticar, TONOS, bitrateLegible, bytesLegibles } from '@/diagnostico'
import { t, formatearNumero } from '@/i18n'

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
</script>

<template>
  <q-card flat bordered class="tarjeta-destino column no-wrap" :class="`tono-${diag.tono}`">
    <!-- Cabecera: identidad y estado, que es lo que se lee de un vistazo. -->
    <div class="cabecera row items-center no-wrap">
      <q-icon
        :name="iArrastrar"
        size="20px"
        class="arrastre text-grey-7"
        :aria-label="t('destino.reordenar', { nombre: destino.name })"
      />
      <!-- Con logo, la imagen identifica el canal y la plataforma baja a sello: se gana
           identidad sin perder de vista a qué servicio va. Sin logo, queda el icono. -->
      <div v-if="logo" class="avatar q-mr-sm">
        <img :src="logo" :alt="t('destino.alt_logo', { nombre: destino.name })" />
        <q-icon :name="plat.icono" size="12px" :style="{ color: plat.color }" class="sello" />
      </div>
      <q-icon v-else :name="plat.icono" size="22px" :style="{ color: plat.color }" class="q-mr-sm" />
      <div class="col nombre ellipsis">{{ destino.name }}</div>
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

    <!-- Estado: con icono y texto, nunca solo color. -->
    <div class="row items-center q-gutter-xs q-px-md">
      <q-chip dense square :color="tono.color" text-color="white" :icon="tono.icono" size="sm"
              class="q-ml-none">
        {{ t(diag.tituloKey) }}
      </q-chip>
      <span v-if="conCifras" class="bitrate">{{ bitrateLegible(m.bitrate_bps) }}</span>
    </div>

    <div v-if="destino.account || conProveedor" class="row items-center q-gutter-xs q-px-md q-pt-xs">
      <q-chip v-if="destino.account" dense square size="sm" :icon="iCuenta"
              :color="destino.account.status === 'reauth' ? 'warning' : 'grey-8'" text-color="white">
        {{ destino.account.display_name }}
        <q-tooltip>{{ destino.account.status === 'reauth' ? t('destino.cuenta_reconectar') : t('destino.cuenta_conectada') }}</q-tooltip>
      </q-chip>
      <q-chip v-else-if="conProveedor" dense square size="sm" color="grey-9" text-color="grey-5">{{ t('destino.sin_cuenta') }}</q-chip>
    </div>

    <div v-if="diag.detalleKey" class="detalle q-px-md q-pt-xs" :class="`text-${tono.color}`">
      {{ t(diag.detalleKey, diag.params) }}
    </div>
    <div v-if="diag.consejoKey" class="consejo q-px-md q-pt-xs">
      <q-icon :name="iConsejo" size="14px" class="q-mr-xs" />{{ t(diag.consejoKey, diag.params) }}
    </div>
    <div v-if="suspendido" class="q-px-md q-pt-sm">
      <q-btn dense no-caps unelevated color="primary" size="sm" :icon="iRotar"
             :label="t('destino.reintentar')" @click="$emit('reintentar')" />
    </div>

    <!-- El detalle técnico va al final y en gris: importa cuando algo falla, no antes. -->
    <div class="col" />
    <div class="pie q-px-md q-pb-sm q-pt-sm">
      <div class="mono ellipsis" :title="destino.rtmp_url">{{ destino.rtmp_url }}</div>
      <div class="row items-center justify-between q-mt-xs">
        <span v-if="destino.key_from_api" class="mono">
          {{ t('destino.clave_api') }}
          <q-tooltip>{{ t('destino.clave_api_tooltip') }}</q-tooltip>
        </span>
        <span v-else class="mono">{{ t('destino.clave_mask', { mascara: destino.key_mask }) }}</span>
        <span v-if="conCifras" class="cifras">
          {{ bytesLegibles(m.bytes_sent) }}
          <template v-if="m.dropped_frames">
            · {{ t('destino.descartes', { n: formatearNumero(m.dropped_frames) }) }}
          </template>
          <template v-if="m.reconnections">
            · {{ t('destino.reconexiones', { n: formatearNumero(m.reconnections) }) }}
          </template>
        </span>
      </div>
      <div v-if="destino.broadcast?.broadcast_ref" class="row items-center q-gutter-sm q-mt-xs">
        <q-btn
          v-if="destino.broadcast.status !== 'live'"
          dense no-caps unelevated color="primary" size="sm"
          :icon="iAlAire" :label="t('destino.salir_al_aire')" @click="$emit('alAire')"
        />
        <q-btn
          v-else
          dense no-caps unelevated color="negative" size="sm"
          :icon="iTerminar" :label="t('destino.terminar')" @click="$emit('terminar')"
        />
        <a
          v-if="destino.broadcast.watch_url"
          :href="destino.broadcast.watch_url"
          target="_blank"
          rel="noopener noreferrer"
          class="enlace-ver text-caption row items-center no-wrap"
        >
          <q-icon :name="iAbrir" size="12px" class="q-mr-xs" />{{ t('destino.ver_en_youtube') }}
        </a>
      </div>
    </div>
  </q-card>
</template>

<style scoped>
.tarjeta-destino {
  border-left: 3px solid transparent;
  transition: border-color 200ms ease;
  height: 100%;
}
.tarjeta-destino.tono-emitiendo { border-left-color: var(--q-positive); }
.tarjeta-destino.tono-atencion  { border-left-color: var(--q-warning); }
.tarjeta-destino.tono-fallo     { border-left-color: var(--q-negative); }
.tarjeta-destino.tono-trabajando{ border-left-color: var(--q-info); }

.avatar {
  position: relative;
  width: 28px;
  height: 28px;
  flex: none;
}
.avatar img {
  width: 28px;
  height: 28px;
  border-radius: 6px;
  object-fit: cover;
  display: block;
  /* El logo lo elige el usuario y puede ser casi blanco o casi negro: el borde tenue lo
     separa del fondo de la tarjeta en los dos casos. */
  background: rgba(255, 255, 255, 0.06);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.12);
}
.avatar .sello {
  position: absolute;
  right: -3px;
  bottom: -3px;
  background: var(--q-dark-page, #1d1d1d);
  border-radius: 50%;
  padding: 1px;
}

.cabecera {
  padding: 8px 8px 4px 4px;
  gap: 2px;
}
.nombre {
  font-size: 15px;
  font-weight: 500;
}
.bitrate {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  color: rgba(255, 255, 255, 0.7);
}
.detalle { font-size: 12px; line-height: 1.4; }
.consejo {
  font-size: 12px;
  line-height: 1.45;
  color: rgba(255, 255, 255, 0.62);
}
.pie {
  border-top: 1px solid rgba(255, 255, 255, 0.06);
  margin-top: 8px;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: rgba(255, 255, 255, 0.5);
}
.cifras {
  font-size: 11px;
  /* Tabulares: se actualizan cada segundo y sin esto los números bailan. */
  font-variant-numeric: tabular-nums;
  color: rgba(255, 255, 255, 0.62);
}
.arrastre {
  cursor: grab;
  touch-action: none;
  /* Área táctil por encima del icono, que es pequeño a propósito. */
  padding: 10px 4px;
}
.enlace-ver {
  color: rgba(255, 255, 255, 0.7);
  text-decoration: none;
}
.enlace-ver:hover {
  text-decoration: underline;
}
@media (prefers-reduced-motion: reduce) {
  .tarjeta-destino { transition: none; }
}
</style>
