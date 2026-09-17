<script setup>
import { computed } from 'vue'
import { iRegistro } from '@/iconos'
import { usePanel } from '@/stores/panel'
import { t, formatearFecha } from '@/i18n'
import ChipEstado from '@/components/ChipEstado.vue'

// El «panel de log en vivo» del spec base §10, que hasta la v0.8 nadie había pintado. Se
// alimenta de recent_events, que viaja con el estado: no hay petición aparte.
const panel = usePanel()

// El tono es fijo; la etiqueta se resuelve con t() dentro del computed de abajo para que
// reaccione al cambio de idioma (un objeto de módulo con el texto ya resuelto no lo haría).
const NIVEL_TONO = { info: 'neutro', warn: 'atencion', error: 'fallo' }
const NIVEL_CLAVE = { info: 'registro.nivel_info', warn: 'registro.nivel_aviso', error: 'registro.nivel_error' }

const nombreDestino = (id) => panel.destinos.find((d) => d.id === id)?.name ?? null

const filas = computed(() => panel.eventosRecientes.map((e) => ({
  ...e,
  nivel: {
    tono: NIVEL_TONO[e.level] ?? NIVEL_TONO.info,
    etiqueta: t(NIVEL_CLAVE[e.level] ?? NIVEL_CLAVE.info),
  },
  destino: e.destination_id ? nombreDestino(e.destination_id) : null,
  hora: formatearFecha(e.created_at, { timeStyle: 'medium' }),
})))
</script>

<template>
  <q-card flat bordered>
    <div class="cabecera-tarjeta row items-center no-wrap">
      <q-icon :name="iRegistro" size="20px" class="q-mr-sm" aria-hidden="true" />
      <span class="ss-t-16 titulo-texto">{{ t('registro.titulo') }}</span>
      <q-space />
      <span class="ss-t-12 ss-subtle ss-tabular">{{ t('registro.ultimos', { n: filas.length }) }}</span>
    </div>
    <div class="registro">
      <div v-for="e in filas" :key="e.id" class="fila">
        <span class="hora ss-mono ss-t-12 ss-subtle ss-tabular">{{ e.hora }}</span>
        <ChipEstado class="nivel" tam="sm" :tono="e.nivel.tono" :texto="e.nivel.etiqueta" />
        <span class="mensaje ss-t-14">
          <span v-if="e.destino" class="text-weight-medium">{{ e.destino }} · </span>{{ e.message }}
        </span>
      </div>
      <div v-if="!filas.length" class="vacio ss-t-14 ss-muted">{{ t('registro.vacio') }}</div>
    </div>
  </q-card>
</template>

<style scoped>
/* Patrón de cabecera de tarjeta (spec v1.1 §3.3), repetido a propósito en cada componente. */
.cabecera-tarjeta {
  padding: var(--ss-space-3) var(--ss-space-4);
  border-bottom: 1px solid var(--ss-border);
}
.titulo-texto { font-weight: 600; }
.registro { max-height: 320px; overflow-y: auto; }
.fila {
  display: grid;
  grid-template-columns: 72px 84px 1fr;
  grid-template-areas: "hora nivel mensaje";
  align-items: center;
  column-gap: var(--ss-space-2);
  padding: var(--ss-space-2) var(--ss-space-4);
  border-bottom: 1px solid var(--ss-border);
}
.fila .hora { grid-area: hora; }
.fila .nivel { grid-area: nivel; justify-self: start; }
.fila .mensaje { grid-area: mensaje; white-space: normal; }
/* Por debajo de 600 px el nivel no cabe en su propia columna: baja a una segunda línea,
   bajo el mensaje, que ocupa las dos filas gracias a compartir el área "mensaje". */
@media (max-width: 599px) {
  .fila {
    grid-template-columns: 72px 1fr;
    grid-template-areas: "hora mensaje" "nivel mensaje";
    row-gap: var(--ss-space-1);
  }
}
.vacio { padding: var(--ss-space-4); }
</style>
