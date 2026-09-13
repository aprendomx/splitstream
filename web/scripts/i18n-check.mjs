#!/usr/bin/env node
// Paridad de idiomas: es.json y en.json con las mismas claves, ninguna vacía, y toda
// clave t('...') literal del código existe. Sin dependencias: corre en prebuild y en CI.
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..', 'src')
const es = JSON.parse(readFileSync(join(raiz, 'i18n', 'es.json'), 'utf8'))
const en = JSON.parse(readFileSync(join(raiz, 'i18n', 'en.json'), 'utf8'))
const errores = []
for (const k of Object.keys(es)) if (!(k in en)) errores.push(`falta en en.json: ${k}`)
for (const k of Object.keys(en)) if (!(k in es)) errores.push(`sobra en en.json (no está en es.json): ${k}`)
for (const [k, v] of [...Object.entries(es), ...Object.entries(en)]) if (!String(v).trim()) errores.push(`vacía: ${k}`)

function archivos(dir) {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    return statSync(p).isDirectory() ? archivos(p) : /\.(vue|js)$/.test(n) ? [p] : []
  })
}
const usadas = new Set()
for (const f of archivos(raiz)) {
  const src = readFileSync(f, 'utf8')
  for (const m of src.matchAll(/\bt\(\s*'([a-z0-9_.]+)'/g)) usadas.add(m[1])
}
for (const k of usadas) if (!(k in es)) errores.push(`clave usada sin definir: ${k}`)
const sinUsar = Object.keys(es).filter((k) => !usadas.has(k) && !usadas.has(k.replace(/_plural$/, '')))
if (errores.length) { console.error(errores.join('\n')); process.exit(1) }
console.log(`i18n: ${Object.keys(es).length} claves, ${usadas.size} usadas, ${sinUsar.length} sin uso literal`)
