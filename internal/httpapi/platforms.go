package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

var nombresPlataforma = map[platforms.ID]string{platforms.Twitch: "Twitch", platforms.YouTube: "YouTube", platforms.Kick: "Kick"}

func capsDTO(c platforms.Capabilities) capabilitiesDTO {
	return capabilitiesDTO{
		Title: c.Title, Category: c.Category, Chat: c.ChatRead,
		Schedule: c.Schedule, IngestKey: c.IngestKey,
		RequiresOwnApp: c.RequiresOwnApp, RequiresPublicURL: c.RequiresPublicURL,
	}
}

// publicURLOK dice si esta instalación cumple lo que la plataforma exige de URL pública.
// Solo el TLS integrado cuenta: con un proxy delante el binario no sabe por qué nombre lo
// alcanzan, así que no puede dar una URL de webhook que vaya a funcionar.
func (s *Server) publicURLOK(c platforms.Capabilities) bool {
	return !c.RequiresPublicURL || (s.tls && s.publicURL != "")
}

// webhookURL es dónde le decimos a Kick que nos llame con el chat. Vacía si esta
// instalación no tiene URL pública propia: entonces no se suscribe nada.
func (s *Server) webhookURL() string {
	if !s.tls || s.publicURL == "" {
		return ""
	}
	return strings.TrimSuffix(s.publicURL, "/") + "/api/platforms/kick/webhook"
}

func (s *Server) handleListPlatforms(w http.ResponseWriter, r *http.Request) {
	out := []platformDTO{}
	if s.platforms != nil {
		for _, p := range s.platforms.All() {
			caps := p.Capabilities()
			out = append(out, platformDTO{ID: string(p.ID()), Name: nombresPlataforma[p.ID()],
				Capabilities: capsDTO(caps), Configured: p.Configured(), PublicURLOK: s.publicURLOK(caps)})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// authFlows son los flujos de dispositivo en curso, en memoria. Un reinicio los pierde:
// el código dura 30 min y se vuelve a empezar. El device_code nunca sale de aquí.
type authFlows struct {
	mu    sync.Mutex
	flows map[string]*authFlow
}

type authFlow struct {
	// status: pending | exchanging | done | expired | error. `exchanging` es el flujo con
	// redirect que YA tiene un callback canjeando su código; existe para que una segunda
	// llegada del mismo código —un doble clic, un reintento del navegador— no vuelva a
	// canjearlo. Hacia fuera se cuenta como `pending`: al panel no le dice nada nuevo.
	status   string
	account  *store.Account
	message  string
	expira   time.Time
	cancel   context.CancelFunc
	platform platforms.ID
	// creds y prompt son los secretos del flujo: las credenciales de la app propia y —en
	// el flujo con redirect— el code_verifier de PKCE y la redirect_uri con la que hay que
	// canjear el código. Viven aquí, en memoria, mientras dura el flujo, y no salen por la
	// API: el callback los necesita para completar el intercambio.
	creds  platforms.Credentials
	prompt platforms.AuthPrompt
	// redirect distingue el flujo con vuelta por el navegador (Kick) del de dispositivo:
	// el primero no tiene goroutine sondeando, lo termina el callback.
	redirect bool
}

// vivo dice si el flujo sigue esperando un desenlace. Debe llamarse con auths.mu tomado.
func (f *authFlow) vivo() bool { return f.status == "pending" || f.status == "exchanging" }

// maxFlujosPorPlataforma tope de flujos "pending" simultáneos por plataforma. Cada uno es
// una goroutine sondeando de por vida (hasta 30 min): sin tope, refrescar la pestaña del
// panel muchas veces podría acumular goroutines sin límite.
const maxFlujosPorPlataforma = 8

// Wait bloquea hasta que todas las goroutines de sondeo de autorización en curso terminen
// —porque acabaron, vencieron, o porque BaseContext se canceló—. main.go la llama antes de
// cerrar la base, para no dejar una goroutine escribiendo en una conexión ya cerrada.
func (s *Server) Wait() { s.wg.Wait() }

// limpiarVencidos borra del mapa las entradas —EN CUALQUIER ESTADO— cuya expiración quedó
// atrás hace más de un minuto.
//
// Antes solo miraba `pending`, pero sondear() deja el flujo en done/expired/error mucho
// antes de que venza su código (el propio sondeo termina en cuanto la persona autoriza o
// el código caduca), así que esa condición nunca se cumplía y el mapa solo se limpiaba de
// las entradas "pending" cuyo estado no había cambiado en absoluto — nada práctico. Solo
// `expira` importa. Debe llamarse con auths.mu ya tomado.
func (s *Server) limpiarVencidos() {
	ahora := time.Now()
	for k, v := range s.auths.flows {
		if ahora.After(v.expira.Add(time.Minute)) {
			delete(s.auths.flows, k)
		}
	}
}

// flujosVivos cuenta los flujos "pending" de una plataforma, tras barrer los vencidos.
func (s *Server) flujosVivos(id platforms.ID) int {
	s.auths.mu.Lock()
	defer s.auths.mu.Unlock()
	s.limpiarVencidos()
	var n int
	for _, v := range s.auths.flows {
		if v.platform == id && v.vivo() {
			n++
		}
	}
	return n
}

type authStartDTO struct {
	State           string `json:"state"`
	VerificationURI string `json:"verification_uri"`
	UserCode        string `json:"user_code"`
	// RedirectURL es la página de la plataforma que el panel abre en el flujo con
	// redirect; vacía en el de dispositivo. Lleva el state firmado, no secretos nuestros.
	RedirectURL string `json:"redirect_url"`
	ExpiresIn   int    `json:"expires_in"`
}

// authStartRequest es el cuerpo OPCIONAL del inicio de autorización: las credenciales de
// la app propia (YouTube, Kick) y el origen desde el que se abrió el panel, que es a donde
// tiene que volver el navegador. El client_secret entra y se convierte en crypto.Secret en
// el acto: nunca se guarda como string ni se registra.
type authStartRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Origin       string `json:"origin"`
}

type authStatusDTO struct {
	Status  string      `json:"status"`
	Account *accountDTO `json:"account"`
	Message string      `json:"message"`
}

func (s *Server) proveedor(w http.ResponseWriter, r *http.Request) (platforms.Provider, bool) {
	if s.platforms == nil {
		writeError(w, http.StatusNotFound, codeNotFound, "plataforma sin proveedor")
		return nil, false
	}
	p, ok := s.platforms.Get(platforms.ID(r.PathValue("p")))
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "plataforma sin proveedor")
		return nil, false
	}
	return p, true
}

// handleStartAuth arranca el flujo y lo sondea en segundo plano: el panel solo consulta
// el estado. Así el device_code y el sondeo viven en el servidor, y cerrar la pestaña no
// cancela nada (la persona puede estar autorizando desde el móvil).
func (s *Server) handleStartAuth(w http.ResponseWriter, r *http.Request) {
	p, ok := s.proveedor(w, r)
	if !ok {
		return
	}
	if s.flujosVivos(p.ID()) >= maxFlujosPorPlataforma {
		writeError(w, http.StatusConflict, codeConflict,
			"hay demasiadas conexiones en curso; espera a que terminen o venzan")
		return
	}
	// El cuerpo es opcional: Twitch usa la app incluida y el panel no manda nada. Solo se
	// intenta decodificar si de verdad viene algo, para que un POST sin cuerpo no sea 400.
	var in authStartRequest
	if r.ContentLength > 0 && !decodeBody(w, r, &in) {
		return
	}
	in.ClientID, in.ClientSecret = strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ClientSecret)
	if p.Capabilities().RequiresOwnApp && (in.ClientID == "" || in.ClientSecret == "") {
		writeError(w, http.StatusBadRequest, codeInvalidInput,
			"esta plataforma necesita las credenciales de tu propia app")
		return
	}
	creds := platforms.Credentials{ClientID: crypto.Secret(in.ClientID), ClientSecret: crypto.Secret(in.ClientSecret)}

	if ra, ok := p.(platforms.RedirectAuth); ok {
		s.empezarRedirect(w, r, p, ra, creds, in.Origin)
		return
	}

	prompt, err := p.BeginAuth(r.Context(), creds)
	if errors.Is(err, platforms.ErrNoClientID) {
		writeError(w, http.StatusConflict, codeConflict,
			"esta plataforma no tiene client_id: pon SPLITSTREAM_TWITCH_CLIENT_ID o espera a una versión con la app incluida")
		return
	}
	if err != nil {
		s.logger.Warn("no se pudo iniciar la autorización", "plataforma", p.ID(), "err", err)
		writeError(w, http.StatusBadGateway, codeInternal, "la plataforma no respondió")
		return
	}
	// El sondeo cuelga de BaseContext, no de la petición HTTP: cerrar la pestaña no debe
	// cancelarlo (la persona puede estar autorizando desde el móvil), pero apagar el
	// proceso sí — y Wait() deja que main.go espere a que esa cancelación surta efecto.
	ctx, cancel := context.WithDeadline(s.baseCtx, prompt.ExpiresAt)
	f := &authFlow{status: "pending", expira: prompt.ExpiresAt, cancel: cancel, platform: p.ID(), creds: creds, prompt: prompt}
	s.auths.mu.Lock()
	s.auths.flows[prompt.State] = f
	s.auths.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.sondear(ctx, p, creds, prompt, f)
	}()

	writeJSON(w, http.StatusOK, authStartDTO{State: prompt.State, VerificationURI: prompt.VerificationURI,
		UserCode: prompt.UserCode, ExpiresIn: int(time.Until(prompt.ExpiresAt).Seconds())})
}

// empezarRedirect arranca el flujo que vuelve por el navegador (Kick). No hay sondeo: el
// flujo se queda pendiente hasta que el callback lo termina o hasta que vence, y el panel
// consulta su estado como en el de dispositivo.
//
// El flujo se guarda bajo un identificador propio, NO bajo el state: el state es público
// —viaja por la barra del navegador y por la plataforma— y lleva su propia firma; la
// clave del mapa es lo que el callback deduce de esa firma.
func (s *Server) empezarRedirect(w http.ResponseWriter, r *http.Request, p platforms.Provider,
	ra platforms.RedirectAuth, creds platforms.Credentials, origen string,
) {
	base, ok := s.origenPermitido(origen)
	if !ok {
		writeError(w, http.StatusBadRequest, codeInvalidInput,
			"no se puede volver a esa dirección; abre el panel por su URL pública o desde esta misma máquina")
		return
	}
	flowID, err := nuevoFlowID()
	if err != nil {
		s.logger.Error("no se pudo generar el identificador del flujo", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "error interno")
		return
	}
	prompt, err := ra.BeginRedirect(r.Context(), creds, base+"/api/platforms/"+string(p.ID())+"/callback", s.firmarState(flowID))
	if errors.Is(err, platforms.ErrNoClientID) {
		writeError(w, http.StatusConflict, codeConflict, "esta plataforma necesita las credenciales de tu propia app")
		return
	}
	if err != nil {
		s.logger.Warn("no se pudo iniciar la autorización", "plataforma", p.ID(), "err", err)
		writeError(w, http.StatusBadGateway, codeInternal, "la plataforma no respondió")
		return
	}
	// El flujo no puede durar más que su state: pasado el TTL, el callback lo rechazaría
	// igual, y dejarlo en el mapa hasta la hora que diga la plataforma solo sirve para que
	// el panel siga esperando por algo que ya no puede llegar.
	expira := s.now().Add(stateTTL)
	if !prompt.ExpiresAt.IsZero() && prompt.ExpiresAt.Before(expira) {
		expira = prompt.ExpiresAt
	}
	f := &authFlow{status: "pending", expira: expira, cancel: func() {}, platform: p.ID(),
		creds: creds, prompt: prompt, redirect: true}
	s.auths.mu.Lock()
	s.auths.flows[flowID] = f
	s.auths.mu.Unlock()

	writeJSON(w, http.StatusOK, authStartDTO{State: flowID, RedirectURL: prompt.RedirectURL,
		ExpiresIn: int(time.Until(expira).Seconds())})
}

// nuevoFlowID son 16 bytes de azar en hexadecimal: la clave del flujo en el mapa, y lo que
// el panel usa para consultar su estado.
func nuevoFlowID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Server) sondear(ctx context.Context, p platforms.Provider, creds platforms.Credentials, prompt platforms.AuthPrompt, f *authFlow) {
	defer f.cancel()
	// Un proveedor que devuelva Interval <= 0 no debe convertir el sondeo en un bucle
	// cerrado machacando PollAuth: 5 s es una espera prudente si no dice nada.
	intervalo := prompt.Interval
	if intervalo <= 0 {
		intervalo = 5 * time.Second
	}
	espera := intervalo
	for {
		select {
		case <-ctx.Done():
			s.terminar(f, "expired", nil, "el código venció; vuelve a empezar")
			return
		case <-time.After(espera):
		}
		nueva, err := p.PollAuth(ctx, creds, prompt)
		switch {
		case err == nil:
			acct, err := s.guardarCuenta(ctx, p, nueva)
			if err != nil {
				s.terminar(f, "error", nil, "no se pudo guardar la cuenta")
				return
			}
			s.terminar(f, "done", acct, "")
			return
		case errors.Is(err, platforms.ErrAuthPending):
			// slow_down no está documentado por Twitch; si el proveedor lo tradujo a
			// pendiente, doblar la espera no cuesta nada.
			if espera < 30*time.Second {
				espera += intervalo / 2
			}
		case errors.Is(err, platforms.ErrAuthExpired):
			s.terminar(f, "expired", nil, "el código venció; vuelve a empezar")
			return
		default:
			if ctx.Err() != nil {
				s.terminar(f, "expired", nil, "el código venció; vuelve a empezar")
				return
			}
			s.logger.Debug("sondeo de autorización falló; se reintenta", "err", err)
		}
	}
}

func (s *Server) terminar(f *authFlow, status string, acct *store.Account, msg string) {
	s.auths.mu.Lock()
	defer s.auths.mu.Unlock()
	// `exchanging` también se puede terminar: es el estado en el que el callback deja el
	// flujo mientras canjea el código, y es él quien viene luego a cerrarlo.
	if f.vivo() {
		f.status, f.account, f.message = status, acct, msg
	}
}

// guardarCuenta persiste la cuenta recién autorizada y deja constancia. Lo comparten el
// flujo de dispositivo y el de redirect: los dos acaban con un store.NewAccount que el
// proveedor ya rellenó (incluidas OwnApp y las credenciales de la app propia).
func (s *Server) guardarCuenta(ctx context.Context, p platforms.Provider, nueva store.NewAccount) (*store.Account, error) {
	acct, err := s.db.UpsertAccount(ctx, s.cipher, nueva)
	if err != nil {
		return nil, err
	}
	s.db.LogEvent(context.WithoutCancel(ctx), store.Event{Level: store.LevelInfo, Kind: "account_connected",
		Message: "cuenta de " + nombresPlataforma[p.ID()] + " conectada: " + acct.DisplayName})
	return acct, nil
}

const (
	// statePrefix versiona el `state` del flujo con redirect y —lo importante— lo separa
	// del prefijo de la cookie de sesión: los dos se firman con la misma clave, así que
	// sin prefijos distintos un state válido podría colarse como cookie o al revés.
	statePrefix = "st1"
	// stateTTL es lo que vale un state: el tiempo de ir a la plataforma, autorizar y
	// volver. Diez minutos sobran y acotan la ventana en la que un state filtrado sirve.
	stateTTL = 10 * time.Minute
)

var errStateInvalido = errors.New("state inválido")

// firmarState produce "st1.<flowID>.<caducidad unix>.<hmac>". Lo único que lleva es el
// identificador del flujo: los secretos —code_verifier, credenciales— se quedan en el mapa
// de flujos, en memoria.
func (s *Server) firmarState(flowID string) string {
	payload := statePrefix + "." + flowID + "." + strconv.FormatInt(s.now().Add(stateTTL).Unix(), 10)
	return payload + "." + s.signer.sign(payload)
}

// verificarState comprueba firma y caducidad, EN ESE ORDEN: la caducidad viaja en el
// propio state, así que sin una firma nuestra no significa nada. Devuelve el flowID.
func (s *Server) verificarState(state string) (string, error) {
	partes := strings.Split(state, ".")
	if len(partes) != 4 || partes[0] != statePrefix || partes[1] == "" {
		return "", errStateInvalido
	}
	payload := partes[0] + "." + partes[1] + "." + partes[2]
	// hmac.Equal compara en tiempo constante, igual que la cookie de sesión.
	if !hmac.Equal([]byte(partes[3]), []byte(s.signer.sign(payload))) {
		return "", errStateInvalido
	}
	exp, err := strconv.ParseInt(partes[2], 10, 64)
	if err != nil {
		return "", errStateInvalido
	}
	if s.now().After(time.Unix(exp, 0)) {
		return "", errStateInvalido
	}
	return partes[1], nil
}

// origenPermitido valida el origen al que el navegador tiene que volver y lo devuelve
// normalizado (esquema y host, sin barra final).
//
// Con TLS integrado el servidor SÍ sabe por qué nombre lo alcanzan, así que solo acepta
// ese —o el propio equipo, para quien abre el panel en local—: aceptar cualquier host
// dejaría que una página ajena montara un redirect_uri hacia sí misma. Sin TLS integrado
// hay un proxy delante con un nombre que este proceso no conoce, y lo único que se puede
// exigir es que el origen esté bien formado.
func (s *Server) origenPermitido(origen string) (string, bool) {
	origen = strings.TrimSuffix(strings.TrimSpace(origen), "/")
	if origen == "" {
		return "", false
	}
	u, err := url.Parse(origen)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	limpio := u.Scheme + "://" + u.Host
	if !s.tls || s.publicURL == "" {
		return limpio, true
	}
	if limpio == strings.TrimSuffix(s.publicURL, "/") {
		return limpio, true
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") {
		return limpio, true
	}
	return "", false
}

// handleAuthCallback es la vuelta del navegador desde la plataforma. Es PÚBLICA: quien
// llega puede no traer la cookie del panel. Lo que la protege es el state firmado y que
// exista un flujo con redirect pendiente que le corresponda.
//
// Ni el código ni el state se registran nunca, y los errores que se le enseñan a quien
// llega no dicen cuál de las comprobaciones falló: con este endpoint abierto, contarlo
// sería explicarle a quien prueba a ciegas por dónde va.
func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	flowID, err := s.verificarState(q.Get("state"))
	if err != nil {
		s.callbackInvalido(w)
		return
	}
	var p platforms.Provider
	if s.platforms != nil {
		p, _ = s.platforms.Get(platforms.ID(r.PathValue("p")))
	}
	ra, _ := p.(platforms.RedirectAuth)
	if ra == nil {
		s.callbackInvalido(w)
		return
	}

	// El flujo se RECLAMA con el candado puesto: se comprueba que está esperando y se deja
	// en `exchanging` antes de soltarlo. Sin eso, dos llegadas del mismo código —un doble
	// clic, un reintento del navegador— verían las dos un flujo `pending` y lo canjearían
	// las dos, porque el canje tarda y ocurre fuera del candado. Los secretos se copian
	// aquí mismo, como en handleAuthStatus.
	s.auths.mu.Lock()
	f, ok := s.auths.flows[flowID]
	// `pending` exacto, no vivo(): un flujo ya en `exchanging` es precisamente el que hay
	// que rechazar, porque su código lo está canjeando otra petición.
	if ok && (f.platform != p.ID() || !f.redirect || f.status != "pending") {
		ok = false
	}
	var creds platforms.Credentials
	var prompt platforms.AuthPrompt
	if ok {
		creds, prompt = f.creds, f.prompt
		f.status = "exchanging"
	}
	s.auths.mu.Unlock()
	if !ok {
		s.callbackInvalido(w)
		return
	}

	if motivo := q.Get("error"); motivo != "" {
		detalle := textoSeguro(q.Get("error_description"))
		if detalle == "" {
			detalle = textoSeguro(motivo)
		}
		s.terminar(f, "error", nil, "la plataforma no autorizó la conexión: "+detalle)
		s.paginaCallback(w, "No se pudo conectar: "+detalle)
		return
	}
	code := q.Get("code")
	if code == "" {
		// El flujo ya está reclamado: se cierra en error en vez de dejarlo en `exchanging`
		// hasta que venza, que al panel le parecería que sigue esperando para siempre.
		s.terminar(f, "error", nil, "la plataforma no devolvió el código de autorización")
		s.callbackInvalido(w)
		return
	}

	// El intercambio cuelga de BaseContext y no de esta petición: si quien autorizó cierra
	// la pestaña justo al volver, la cuenta se conecta igual.
	ctx, cancel := context.WithTimeout(s.baseCtx, 30*time.Second)
	defer cancel()
	nueva, err := ra.CompleteRedirect(ctx, creds, prompt, code)
	if err != nil {
		// Sin el error del proveedor en el log: es la única vía por la que el código
		// podría acabar escrito en disco si algún día alguien lo incluyera en el texto.
		s.logger.Warn("no se pudo completar la autorización", "plataforma", p.ID())
		// El mensaje acaba en el panel y puede arrastrar texto de la plataforma dentro del
		// error: pasa por textoSeguro como el error_description de la query.
		s.terminar(f, "error", nil, textoSeguro(mensajePlataforma(err)))
		s.paginaCallback(w, "No se pudo conectar. Vuelve al panel e inténtalo otra vez.")
		return
	}
	acct, err := s.guardarCuenta(ctx, p, nueva)
	if err != nil {
		s.logger.Error("no se pudo guardar la cuenta", "plataforma", p.ID(), "err", err)
		s.terminar(f, "error", nil, "no se pudo guardar la cuenta")
		s.paginaCallback(w, "No se pudo conectar. Vuelve al panel e inténtalo otra vez.")
		return
	}
	s.suscribirChat(ctx, p, acct)
	s.terminar(f, "done", acct, "")
	s.paginaCallback(w, "Cuenta conectada. Ya puedes cerrar esta pestaña y volver al panel.")
}

// suscribirChat pide a la plataforma que nos mande el chat por webhook. Solo tiene sentido
// con URL pública propia; si falla, la cuenta queda conectada igual y se deja constancia:
// no poder leer el chat no es razón para tirar una conexión que ya funciona.
func (s *Server) suscribirChat(ctx context.Context, p platforms.Provider, acct *store.Account) {
	cw, ok := p.(platforms.ChatWebhook)
	if !ok {
		return
	}
	url := s.webhookURL()
	if url == "" || s.tokens == nil {
		return
	}
	aviso := func(err error) {
		s.db.LogEvent(context.WithoutCancel(ctx), store.Event{Level: store.LevelWarn, Kind: "chat_disconnected",
			Message: "no se pudo suscribir el chat de " + nombresPlataforma[p.ID()] + ": " + mensajePlataforma(err)})
	}
	tok, err := s.tokens.Token(ctx, acct.ID)
	if err != nil {
		aviso(err)
		return
	}
	if err := cw.SubscribeChat(ctx, *acct, tok, url); err != nil {
		aviso(err)
	}
}

// callbackInvalido es la única respuesta de error que ve quien llega con un state que no
// vale: 400 en texto plano y sin decir por qué.
func (s *Server) callbackInvalido(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte("solicitud inválida\n"))
}

// paginaCallback es la página que cierra el flujo. Sin scripts, sin estilos externos y con
// una CSP que no deja cargar nada: el único contenido variable es un texto que pasa por
// textoSeguro y por el escapado de HTML.
func (s *Server) paginaCallback(w http.ResponseWriter, mensaje string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("<!doctype html><html lang=\"es\"><head><meta charset=\"utf-8\">" +
		"<title>Splitstream</title></head><body><p>" + html.EscapeString(mensaje) + "</p></body></html>\n"))
}

// textoSeguro deja un texto de la plataforma en algo que se puede enseñar: sin caracteres
// de control y acotado, porque lo escribe alguien de fuera.
func textoSeguro(s string) string {
	limpio := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(limpio) > 200 {
		limpio = limpio[:200]
	}
	return strings.ToValidUTF8(limpio, "")
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	p, ok := s.proveedor(w, r)
	if !ok {
		return
	}
	state := r.PathValue("state")
	s.auths.mu.Lock()
	f, ok := s.auths.flows[state]
	// Cada flujo pertenece a la plataforma en la que se inició: consultarlo desde la ruta
	// de otra no puede devolver su estado ni su cuenta. Se comprueba ANTES de entregar y
	// borrar, para que una consulta con la plataforma equivocada no se lleve por delante
	// el flujo bueno.
	if ok && f.platform != p.ID() {
		ok = false
	}
	if ok && !f.vivo() {
		// Un flujo terminado se entrega una vez y se olvida; los expirados sin consultar
		// se limpian de paso. Uno en `exchanging` NO se entrega: todavía está en marcha.
		delete(s.auths.flows, state)
	}
	s.limpiarVencidos()
	// Estado, mensaje y cuenta se copian AQUÍ, todavía con el candado puesto: sondear()
	// escribe esos mismos campos desde su goroutine, y leerlos fuera del candado sería una
	// carrera de datos aunque el mapa ya esté a salvo.
	var status, message string
	var account *store.Account
	if ok {
		status, message, account = f.status, f.message, f.account
		// `exchanging` es un detalle de cómo se cierra el flujo por dentro: para quien
		// pregunta sigue siendo una conexión en curso.
		if status == "exchanging" {
			status = "pending"
		}
	}
	s.auths.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "flujo de autorización desconocido")
		return
	}
	out := authStatusDTO{Status: status, Message: message}
	if account != nil {
		dests, _ := s.db.DestinationsOfAccount(r.Context(), account.ID)
		dto := newAccountDTO(*account, dests)
		out.Account = &dto
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	cuentas, err := s.db.Accounts(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	out := make([]accountDTO, 0, len(cuentas))
	for _, a := range cuentas {
		dests, err := s.db.DestinationsOfAccount(r.Context(), a.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		dto := newAccountDTO(a, dests)
		dto.QuotaUsedToday = s.cuotaDeCuenta(r.Context(), a)
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, out)
}

// cuotaDeCuenta devuelve las unidades gastadas hoy, o nil si no aplica. La cuota es de
// YouTube: es la única plataforma que la reparte por app propia y la única que puede
// quedarse sin ella a mitad de un directo. La plataforma se mira por su id, no importando
// el paquete del proveedor (la CI comprueba que este paquete no lo haga).
func (s *Server) cuotaDeCuenta(ctx context.Context, a store.Account) *int {
	if s.quota == nil || platforms.ID(a.Platform) != platforms.YouTube {
		return nil
	}
	n, err := s.quota.UsedToday(ctx, a.ID)
	if err != nil {
		s.logger.Debug("no se pudo leer la cuota", "cuenta", a.ID, "err", err)
		return nil
	}
	return &n
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathID(w, r)
	if !ok {
		return
	}
	acct, err := s.db.AccountByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.desuscribirChat(*acct)
	if err := s.db.DeleteAccount(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.db.LogEvent(context.WithoutCancel(r.Context()), store.Event{Level: store.LevelInfo, Kind: "account_disconnected",
		Message: "cuenta de " + nombresPlataforma[platforms.ID(acct.Platform)] + " desconectada: " + acct.DisplayName})
	w.WriteHeader(http.StatusNoContent)
}

// desuscribirChat le dice a la plataforma que deje de mandarnos el chat de una cuenta que
// se va. Es lo correcto por cortesía y porque una suscripción huérfana seguiría llamando a
// nuestro webhook para siempre, pero NO puede impedir el borrado: si falla —el token ya no
// vale, la plataforma no responde— se apunta en el log y se sigue. Sin el token en él.
//
// El plazo cuelga de BaseContext y no de la petición: es trabajo que hay que intentar
// terminar aunque quien pulsó «desconectar» ya se haya ido.
func (s *Server) desuscribirChat(acct store.Account) {
	if s.platforms == nil || s.tokens == nil || acct.Status != store.AccountStatusOK {
		return
	}
	p, ok := s.platforms.Get(platforms.ID(acct.Platform))
	if !ok {
		return
	}
	cw, ok := p.(platforms.ChatWebhook)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(s.baseCtx, 10*time.Second)
	defer cancel()
	tok, err := s.tokens.Token(ctx, acct.ID)
	if err != nil {
		s.logger.Warn("no se pudo cancelar la suscripción al chat", "cuenta", acct.ID)
		return
	}
	if err := cw.UnsubscribeChat(ctx, acct, tok); err != nil {
		s.logger.Warn("no se pudo cancelar la suscripción al chat", "cuenta", acct.ID, "err", err)
	}
}
