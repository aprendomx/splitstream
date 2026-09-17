<script setup>
import { ref, computed } from 'vue'
import { useQuasar } from 'quasar'
import { api, ApiError } from '@/api'
import { usePanel } from '@/stores/panel'
import { iTitulo, iCerrar, iDesplegar } from '@/iconos'
import { t } from '@/i18n'
import ChipEstado from '@/components/ChipEstado.vue'

const $q = useQuasar()
const panel = usePanel()
const titulo = ref('')
const categoria = ref(null)      // {id, name}
const opciones = ref([])
const aplicando = ref(false)
const resultados = ref([])

// Solo los destinos con cuenta y capacidad: es lo que el botón promete.
const candidatos = computed(() => panel.destinos.filter((d) => d.account && (d.capabilities?.title || d.capabilities?.category)))
const hayTwitch = computed(() => candidatos.value.some((d) => d.platform === 'twitch' && d.capabilities?.category))

async function buscar(q, update) {
  if (!q) { update(() => { opciones.value = [] }); return }
  try {
    const cats = await api.categoriasTwitch(q)
    update(() => { opciones.value = cats })
  } catch { update(() => { opciones.value = [] }) }
}

async function aplicar() {
  aplicando.value = true
  resultados.value = []
  try {
    const body = { destinations: candidatos.value.map((d) => d.id) }
    if (titulo.value.trim()) body.title = titulo.value.trim()
    if (categoria.value) body.category_id = categoria.value.id
    resultados.value = await api.aplicarTitulo(body)
    const ok = resultados.value.filter((r) => r.ok).length
    $q.notify({
      type: ok === resultados.value.length ? 'positive' : 'warning',
      message: t('titulo.resultado_notify', { ok, total: resultados.value.length }),
    })
  } catch (e) {
    $q.notify({ type: 'negative', message: e instanceof ApiError ? e.message : t('titulo.error_aplicar') })
  } finally {
    aplicando.value = false
  }
}
const nombreDe = (id) => panel.destinos.find((d) => d.id === id)?.name ?? `#${id}`
</script>

<template>
  <q-card v-if="candidatos.length" flat bordered class="q-mb-md">
    <!-- Cabecera: solo identidad, sin acciones (el botón «Aplicar» vive con el formulario). -->
    <div class="cabecera-tarjeta row items-center no-wrap">
      <q-icon :name="iTitulo" size="20px" class="q-mr-sm" aria-hidden="true" />
      <span class="ss-t-16 titulo-texto">{{ t('titulo.titulo') }}</span>
    </div>
    <q-card-section class="q-gutter-y-sm">
      <div class="row q-col-gutter-sm items-start">
        <div class="col-12 col-sm">
          <q-input
            v-model="titulo"
            outlined
            :label="t('titulo.campo_titulo')"
            maxlength="140"
            counter
            :hint="t('titulo.se_aplica_a', { lista: candidatos.map((d) => d.name).join(', ') })"
          />
        </div>
        <div v-if="hayTwitch" class="col-12 col-sm-5">
          <q-select v-model="categoria" :options="opciones" option-label="name" outlined use-input fill-input hide-selected
                    input-debounce="300" :label="t('titulo.categoria_twitch_label')" clearable :dropdown-icon="iDesplegar" :clear-icon="iCerrar" @filter="buscar">
            <template #option="{ itemProps, opt }">
              <q-item v-bind="itemProps"><q-item-section avatar><img :src="opt.box_art_url" width="24" alt="" /></q-item-section><q-item-section>{{ opt.name }}</q-item-section></q-item>
            </template>
          </q-select>
        </div>
      </div>
      <div class="row items-center">
        <q-space />
        <q-btn unelevated no-caps color="primary" :loading="aplicando" :disable="!titulo.trim() && !categoria"
               :label="t('titulo.aplicar_boton')" @click="aplicar" />
      </div>
      <div v-if="resultados.length" class="column q-gutter-y-xs">
        <div v-for="r in resultados" :key="r.destination_id" class="row items-center q-gutter-sm resultado-fila">
          <ChipEstado tam="sm" :tono="r.ok ? 'emitiendo' : 'fallo'" :texto="nombreDe(r.destination_id)" />
          <span class="ss-t-14 ss-muted">{{ r.message }}</span>
        </div>
      </div>
    </q-card-section>
  </q-card>
</template>

<style scoped>
/* Patrón de cabecera de tarjeta (spec v1.1 §3.3), repetido a propósito en cada componente. */
.cabecera-tarjeta {
  padding: var(--ss-space-3) var(--ss-space-4);
  border-bottom: 1px solid var(--ss-border);
}
.titulo-texto { font-weight: 600; }
</style>
