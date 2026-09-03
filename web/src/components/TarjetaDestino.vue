<script setup>
import { iBorrar, iClave, iConsejo, iEditar, iMenu } from '@/iconos'
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
</script>

<template>
  <q-card flat bordered class="tarjeta-destino" :class="`tono-${diag.tono}`">
    <q-card-section class="row items-start no-wrap q-gutter-sm">
      <q-icon :name="plat.icono" size="26px" :style="{ color: plat.color }" class="q-mt-xs" />

      <div class="col">
        <div class="row items-center q-gutter-xs">
          <span class="text-subtitle1 text-weight-medium">{{ destino.name }}</span>
          <!-- El estado NO se comunica solo por color: lleva icono y texto. -->
          <q-chip dense square :color="tono.color" text-color="white" :icon="tono.icono" size="sm">
            {{ diag.titulo }}
          </q-chip>
        </div>

        <div class="text-caption text-grey-5 ellipsis">{{ destino.rtmp_url }}</div>
        <div class="text-caption text-grey-6">clave {{ destino.key_mask }}</div>

        <div v-if="diag.detalle" class="text-caption q-mt-xs" :class="`text-${tono.color}`">
          {{ diag.detalle }}
        </div>
        <div v-if="diag.consejo" class="consejo q-mt-xs">
          <q-icon :name="iConsejo" size="14px" class="q-mr-xs" />{{ diag.consejo }}
        </div>

        <!-- Cifras solo cuando significan algo. Números tabulares para que no bailen al
             actualizarse cada segundo. -->
        <div v-if="m && haySesion && destino.enabled" class="row q-gutter-md q-mt-sm cifras">
          <div><span class="etiqueta">bitrate</span> {{ bitrateLegible(m.bitrate_bps) }}</div>
          <div><span class="etiqueta">enviado</span> {{ bytesLegibles(m.bytes_sent) }}</div>
          <div v-if="m.dropped_frames">
            <span class="etiqueta">descartes</span> {{ m.dropped_frames.toLocaleString('es') }}
          </div>
          <div v-if="m.reconnections">
            <span class="etiqueta">reconexiones</span> {{ m.reconnections }}
          </div>
        </div>
      </div>

      <div class="column items-center q-gutter-xs">
        <q-toggle
          :model-value="destino.enabled"
          @update:model-value="$emit('alternar')"
          :aria-label="`${destino.enabled ? 'Apagar' : 'Encender'} ${destino.name}`"
        />
        <q-btn flat round dense :icon="iMenu" aria-label="Más acciones">
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
              <!-- Separada del resto: es la única acción irreversible del menú. -->
              <q-item clickable v-close-popup class="text-negative" @click="$emit('borrar')">
                <q-item-section avatar><q-icon :name="iBorrar" /></q-item-section>
                <q-item-section>Eliminar</q-item-section>
              </q-item>
            </q-list>
          </q-menu>
        </q-btn>
      </div>
    </q-card-section>
  </q-card>
</template>

<style scoped>
.tarjeta-destino {
  border-left: 3px solid transparent;
  transition: border-color 200ms ease;
}
.tarjeta-destino.tono-emitiendo { border-left-color: var(--q-positive); }
.tarjeta-destino.tono-atencion  { border-left-color: var(--q-warning); }
.tarjeta-destino.tono-fallo     { border-left-color: var(--q-negative); }
.tarjeta-destino.tono-trabajando{ border-left-color: var(--q-info); }

.consejo {
  font-size: 12px;
  line-height: 1.45;
  color: rgba(255, 255, 255, 0.62);
}
.cifras {
  font-size: 12px;
  /* Tabulares: sin esto los números bailan cada segundo y la vista se cansa. */
  font-variant-numeric: tabular-nums;
  color: rgba(255, 255, 255, 0.78);
}
.cifras .etiqueta {
  color: rgba(255, 255, 255, 0.45);
  margin-right: 3px;
}
@media (prefers-reduced-motion: reduce) {
  .tarjeta-destino { transition: none; }
}
</style>
