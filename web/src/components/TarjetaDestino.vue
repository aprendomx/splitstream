<script setup>
import { iEditar, iBorrar, iClave, iMenu, iConsejo, iArrastrar } from '@/iconos'
import { computed } from 'vue'
import { porId } from '@/plataformas'
import { diagnosticar, TONOS, bitrateLegible, bytesLegibles } from '@/diagnostico'

const props = defineProps({
  destino: { type: Object, required: true },
  haySesion: Boolean,
})
defineEmits(['editar', 'alternar', 'borrar', 'revelar'])

const plat = computed(() => porId(props.destino.platform))
const diag = computed(() => diagnosticar(props.destino, props.haySesion))
const tono = computed(() => TONOS[diag.value.tono])
const m = computed(() => props.destino.metrics)
const conCifras = computed(() => m.value && props.haySesion && props.destino.enabled)
</script>

<template>
  <q-card flat bordered class="tarjeta-destino column no-wrap" :class="`tono-${diag.tono}`">
    <!-- Cabecera: identidad y estado, que es lo que se lee de un vistazo. -->
    <div class="cabecera row items-center no-wrap">
      <q-icon
        :name="iArrastrar"
        size="20px"
        class="arrastre text-grey-7"
        :aria-label="`Reordenar ${destino.name}`"
      />
      <q-icon :name="plat.icono" size="22px" :style="{ color: plat.color }" class="q-mr-sm" />
      <div class="col nombre ellipsis">{{ destino.name }}</div>
      <q-toggle
        :model-value="destino.enabled"
        dense
        @update:model-value="$emit('alternar')"
        :aria-label="`${destino.enabled ? 'Apagar' : 'Encender'} ${destino.name}`"
      />
      <q-btn flat round dense :icon="iMenu" size="sm" aria-label="Más acciones">
        <q-menu anchor="bottom right" self="top right">
          <q-list style="min-width: 190px">
            <q-item clickable v-close-popup @click="$emit('editar')">
              <q-item-section avatar><q-icon :name="iEditar" /></q-item-section>
              <q-item-section>Editar</q-item-section>
            </q-item>
            <q-item clickable v-close-popup @click="$emit('revelar')">
              <q-item-section avatar><q-icon :name="iClave" /></q-item-section>
              <q-item-section>
                Ver la clave
                <q-item-label caption>Queda registrado</q-item-label>
              </q-item-section>
            </q-item>
            <q-separator />
            <q-item clickable v-close-popup class="text-negative" @click="$emit('borrar')">
              <q-item-section avatar><q-icon :name="iBorrar" /></q-item-section>
              <q-item-section>Eliminar</q-item-section>
            </q-item>
          </q-list>
        </q-menu>
      </q-btn>
    </div>

    <!-- Estado: con icono y texto, nunca solo color. -->
    <div class="row items-center q-gutter-xs q-px-md">
      <q-chip dense square :color="tono.color" text-color="white" :icon="tono.icono" size="sm"
              class="q-ml-none">
        {{ diag.titulo }}
      </q-chip>
      <span v-if="conCifras" class="bitrate">{{ bitrateLegible(m.bitrate_bps) }}</span>
    </div>

    <div v-if="diag.detalle" class="detalle q-px-md q-pt-xs" :class="`text-${tono.color}`">
      {{ diag.detalle }}
    </div>
    <div v-if="diag.consejo" class="consejo q-px-md q-pt-xs">
      <q-icon :name="iConsejo" size="14px" class="q-mr-xs" />{{ diag.consejo }}
    </div>

    <!-- El detalle técnico va al final y en gris: importa cuando algo falla, no antes. -->
    <div class="col" />
    <div class="pie q-px-md q-pb-sm q-pt-sm">
      <div class="mono ellipsis" :title="destino.rtmp_url">{{ destino.rtmp_url }}</div>
      <div class="row items-center justify-between q-mt-xs">
        <span class="mono">clave {{ destino.key_mask }}</span>
        <span v-if="conCifras" class="cifras">
          {{ bytesLegibles(m.bytes_sent) }}
          <template v-if="m.dropped_frames">
            · {{ m.dropped_frames.toLocaleString('es') }} descartes
          </template>
          <template v-if="m.reconnections">
            · {{ m.reconnections }} reconexiones
          </template>
        </span>
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
@media (prefers-reduced-motion: reduce) {
  .tarjeta-destino { transition: none; }
}
</style>
