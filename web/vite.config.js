import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { quasar, transformAssetUrls } from '@quasar/vite-plugin'

// Se usa Vite con el plugin de Quasar en vez del CLI de Quasar: el CLI es interactivo al
// crear el proyecto, y eso no sirve para una CI. El resultado es el mismo Quasar 2 sobre
// Vue 3 que pide el spec §5, con un build reproducible y sin preguntas.
export default defineConfig({
  plugins: [
    vue({ template: { transformAssetUrls } }),
    quasar({ sassVariables: fileURLToPath(new URL('./src/css/quasar.variables.scss', import.meta.url)) }),
  ],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    // dist/spa es la ruta que el spec §4 fija para el go:embed del binario.
    outDir: 'dist/spa',
    emptyOutDir: true,
  },
  server: {
    // En desarrollo, la API la sirve el binario en :8099. El proxy evita CORS y hace que
    // la cookie httpOnly SameSite=Lax viaje igual que en producción, donde todo es el
    // mismo origen.
    proxy: {
      '/api': { target: 'http://127.0.0.1:8099', changeOrigin: false },
      '/ws': { target: 'ws://127.0.0.1:8099', ws: true },
    },
  },
})
