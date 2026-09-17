<script setup>
import { computed } from 'vue'
import { TONOS } from '@/diagnostico'
const props = defineProps({
  tono: { type: String, default: 'neutro' },
  icono: { type: String, default: '' },
  texto: { type: String, required: true },
  tam: { type: String, default: 'md' },
  pulso: Boolean,
  // Región viva solo donde el chip cambia solo y conviene que se anuncie (cabecera de
  // sesión). En listas (registro, historial, webhooks, tarjetas) insertar filas
  // constantemente con role="status" hace que el lector de pantalla recite cada una.
  anuncia: Boolean,
})
const icono = computed(() => props.icono || TONOS[props.tono]?.icono)
</script>
<template>
  <!-- Estado siempre con icono y texto (spec §3.4): el color acompaña, no informa solo. -->
  <span class="chip" :class="[`tono-${tono}`, `tam-${tam}`]" :role="anuncia ? 'status' : undefined">
    <span v-if="pulso && tam === 'lg'" class="punto" aria-hidden="true" />
    <q-icon v-else :name="icono" class="icono" aria-hidden="true" />
    <span class="texto">{{ texto }}</span>
  </span>
</template>
<style scoped>
.chip { display: inline-flex; align-items: center; gap: 6px; border-radius: 999px; border: 1px solid var(--ss-border); background: var(--ss-surface-2); color: var(--ss-fg); white-space: nowrap; font-weight: 500; max-width: 100%; }
.tam-sm { font-size: 12px; line-height: 1; padding: 4px 8px; }
.tam-md { font-size: 14px; line-height: 1; padding: 6px 10px; }
.tam-lg { font-size: 16px; line-height: 1; padding: 8px 14px; }
.icono { font-size: 1.15em; flex: none; }
/* Red de seguridad: el texto es casi siempre corto, pero si algún día no lo es (o llega
   texto arbitrario de la API), esto corta con «…» en vez de empujar scroll horizontal. */
.texto { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.tono-emitiendo { border-color: var(--ss-live); color: var(--ss-live); }
.tono-atencion { border-color: var(--ss-warn); color: var(--ss-warn); }
.tono-fallo { border-color: var(--ss-danger); color: var(--ss-danger-fg); }
.tono-trabajando { border-color: var(--ss-info); color: var(--ss-info); }
.tono-neutro { color: var(--ss-fg-muted); }
.punto { width: 10px; height: 10px; border-radius: 50%; background: currentColor; animation: pulso 1.6s var(--ss-ease) infinite; }
@keyframes pulso { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }
@media (prefers-reduced-motion: reduce) { .punto { animation: none; } }
</style>
