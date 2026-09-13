import { ref, watch } from 'vue'
import { Quasar } from 'quasar'
import langEs from 'quasar/lang/es'
import langEn from 'quasar/lang/en-US'
import es from './es.json'
import en from './en.json'

// Un t() propio en vez de vue-i18n: el spec base §5 cierra el frontend a Vue, Quasar,
// Pinia y vuedraggable, y dos diccionarios planos con interpolación y plural por sufijo
// cubren todo lo que el panel necesita. Añadir un idioma es un JSON y una línea aquí.
export const CLAVE_IDIOMA = 'splitstream.idioma'
export const idiomas = [
  { id: 'es', nombre: 'Español' },
  { id: 'en', nombre: 'English' },
]
const diccionarios = { es, en }
const quasarLang = { es: langEs, en: langEn }

function idiomaInicial() {
  try {
    const guardado = localStorage.getItem(CLAVE_IDIOMA)
    if (guardado && diccionarios[guardado]) return guardado
  } catch { /* sin localStorage: se decide por el navegador */ }
  const nav = (navigator.language || 'es').toLowerCase()
  return nav.startsWith('en') ? 'en' : 'es'
}

export const idioma = ref(idiomaInicial())

const avisadas = new Set()
export function t(clave, params = {}) {
  let k = clave
  if (typeof params.n === 'number' && params.n !== 1 && diccionarios[idioma.value][`${clave}_plural`]) k = `${clave}_plural`
  let texto = diccionarios[idioma.value][k] ?? diccionarios.es[k]
  if (texto === undefined) {
    if (import.meta.env.DEV && !avisadas.has(clave)) { avisadas.add(clave); console.warn(`i18n: falta la clave ${clave}`) }
    return clave
  }
  return texto.replace(/\{(\w+)\}/g, (_, nombre) => (params[nombre] ?? `{${nombre}}`))
}

export function cambiarIdioma(id) {
  if (!diccionarios[id]) return
  idioma.value = id
  try { localStorage.setItem(CLAVE_IDIOMA, id) } catch { /* se pierde al recargar; no pasa nada */ }
}

export const formatearFecha = (iso, opciones = { dateStyle: 'medium', timeStyle: 'short' }) =>
  new Intl.DateTimeFormat(idioma.value, opciones).format(new Date(iso))
export const formatearNumero = (n, opciones = {}) => new Intl.NumberFormat(idioma.value, opciones).format(n)

watch(idioma, (id) => {
  Quasar.lang.set(quasarLang[id])
  document.documentElement.lang = id
}, { immediate: true })
