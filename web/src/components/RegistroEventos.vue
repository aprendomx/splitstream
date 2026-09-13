<script setup>
import { computed } from 'vue'
import { iRegistro } from '@/iconos'
import { usePanel } from '@/stores/panel'
import { t, formatearFecha } from '@/i18n'

// El «panel de log en vivo» del spec base §10, que hasta la v0.8 nadie había pintado. Se
// alimenta de recent_events, que viaja con el estado: no hay petición aparte.
const panel = usePanel()

// El color es fijo; la etiqueta se resuelve con t() dentro del computed de abajo para que
// reaccione al cambio de idioma (un objeto de módulo con el texto ya resuelto no lo haría).
const NIVEL_COLOR = { info: 'grey-6', warn: 'warning', error: 'negative' }
const NIVEL_CLAVE = { info: 'registro.nivel_info', warn: 'registro.nivel_aviso', error: 'registro.nivel_error' }

const nombreDestino = (id) => panel.destinos.find((d) => d.id === id)?.name ?? null

const filas = computed(() => panel.eventosRecientes.map((e) => ({
  ...e,
  nivel: {
    color: NIVEL_COLOR[e.level] ?? NIVEL_COLOR.info,
    etiqueta: t(NIVEL_CLAVE[e.level] ?? NIVEL_CLAVE.info),
  },
  destino: e.destination_id ? nombreDestino(e.destination_id) : null,
  hora: formatearFecha(e.created_at, { timeStyle: 'medium' }),
})))
</script>

<template>
  <q-card flat bordered>
    <q-card-section class="row items-center q-py-sm">
      <q-icon :name="iRegistro" size="20px" class="q-mr-sm text-grey-5" />
      <div class="text-subtitle2">{{ t('registro.titulo') }}</div>
      <q-space />
      <div class="text-caption text-grey-6">{{ t('registro.ultimos', { n: filas.length }) }}</div>
    </q-card-section>
    <q-separator />
    <q-list dense class="registro">
      <q-item v-for="e in filas" :key="e.id" class="fila">
        <q-item-section side class="hora">{{ e.hora }}</q-item-section>
        <q-item-section side>
          <q-badge :color="e.nivel.color" :label="e.nivel.etiqueta" class="nivel" />
        </q-item-section>
        <q-item-section>
          <q-item-label class="mensaje">
            <span v-if="e.destino" class="text-weight-medium">{{ e.destino }} · </span>{{ e.message }}
          </q-item-label>
        </q-item-section>
      </q-item>
      <q-item v-if="!filas.length">
        <q-item-section class="text-grey-6 text-caption">{{ t('registro.vacio') }}</q-item-section>
      </q-item>
    </q-list>
  </q-card>
</template>

<style scoped>
.registro { max-height: 320px; overflow-y: auto; }
.hora {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: rgba(255, 255, 255, 0.5);
  min-width: 64px;
}
.nivel { font-size: 10px; min-width: 42px; justify-content: center; }
.mensaje { font-size: 13px; white-space: normal; }
</style>
