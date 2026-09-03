import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { Quasar, Dark, Notify, Dialog } from 'quasar'
// Sin importar el CSS de mdi-v7: los iconos entran uno a uno como SVG (ver src/iconos.js).
import 'quasar/src/css/index.sass'

import App from './App.vue'
import { router } from './router'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(Quasar, {
  plugins: { Notify, Dialog },
  config: {
    // Oscuro por defecto (spec §10). No es una preferencia estética: el panel se abre a
    // mitad de una transmisión, muchas veces de noche.
    dark: true,
    notify: { position: 'top', timeout: 4000 },
  },
})
Dark.set(true)
app.mount('#app')
