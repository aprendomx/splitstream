# Conectar Kick: crea tu propia app de desarrollador

Para que Splitstream conecte tu cuenta de Kick —traer la clave y la URL de ingesta,
cambiar el título y la categoría, y leer el chat por webhook— necesita un `client_id` y un
`client_secret` de una app de Kick **tuya**.

**Por qué se pide.** Kick no admite aplicaciones públicas: exige el `client_secret` en
cada intercambio de tokens (al conectar y al refrescar), así que no puede ir incluido en
el binario de Splitstream como un dato compartido. Cada instalación necesita su propia
app registrada.

---

## Paso 1: abre los ajustes de desarrollador

En Kick, ve a **Ajustes → Developer**. Necesitas la verificación en dos pasos (2FA)
activada en tu cuenta para entrar aquí; si no la tienes, Kick te pedirá activarla primero.

## Paso 2: crea una app nueva

Pulsa **Create app** (o el botón equivalente) y dale un nombre — el que quieras, no lo ve
nadie más que tú.

## Paso 3: Redirect URL

Pega esta dirección **exactamente así**, sin cambiar nada, como Redirect URL de la app. El
panel te la enseña con un botón de copiar; tiene esta forma:

```
<origen del panel>/api/platforms/kick/callback
```

Por ejemplo, en tu propio equipo:

```
http://localhost:8080/api/platforms/kick/callback
```

o, con el panel publicado en un dominio:

```
https://relay.ejemplo.com/api/platforms/kick/callback
```

Kick exige coincidencia exacta con la que registraste aquí: una barra de más o un `http`
en vez de `https` y el paso de autorización falla.

## Paso 4: webhooks del chat (opcional)

Si quieres el chat de Kick en el panel, activa **Enable Webhooks** y pega esta dirección,
también con un botón de copiar en el asistente del panel:

```
<URL pública>/api/platforms/kick/webhook
```

Esto **solo funciona si el panel es accesible por HTTPS desde internet** — con el TLS
integrado de Splitstream (ver «Ponerlo en internet» en el README) o con un proxy delante
que sirva HTTPS público. En un PC sin URL pública, el chat de Kick no está disponible: el
panel te lo dice en el bloque «Cuenta» y no te bloquea nada más, solo faltará el chat.

## Paso 5: copia el client_id y el client_secret

Kick te los muestra al terminar de crear la app. Cópialos y pégalos en el asistente del
panel de Splitstream.
