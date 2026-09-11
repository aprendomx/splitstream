package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

var nombresPlataforma = map[platforms.ID]string{platforms.Twitch: "Twitch", platforms.YouTube: "YouTube", platforms.Kick: "Kick"}

func capsDTO(c platforms.Capabilities) capabilitiesDTO {
	return capabilitiesDTO{Title: c.Title, Category: c.Category, Chat: c.ChatRead}
}

func (s *Server) handleListPlatforms(w http.ResponseWriter, r *http.Request) {
	out := []platformDTO{}
	if s.platforms != nil {
		for _, p := range s.platforms.All() {
			out = append(out, platformDTO{ID: string(p.ID()), Name: nombresPlataforma[p.ID()], Capabilities: capsDTO(p.Capabilities()), Configured: p.Configured()})
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
	status   string // pending | done | expired | error
	account  *store.Account
	message  string
	expira   time.Time
	cancel   context.CancelFunc
	platform platforms.ID
}

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
		if v.platform == id && v.status == "pending" {
			n++
		}
	}
	return n
}

type authStartDTO struct {
	State           string `json:"state"`
	VerificationURI string `json:"verification_uri"`
	UserCode        string `json:"user_code"`
	ExpiresIn       int    `json:"expires_in"`
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
	// TODO(Task 9): pasar las credenciales de la cuenta con app propia en vez de vacías.
	prompt, err := p.BeginAuth(r.Context(), platforms.Credentials{})
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
	f := &authFlow{status: "pending", expira: prompt.ExpiresAt, cancel: cancel, platform: p.ID()}
	s.auths.mu.Lock()
	s.auths.flows[prompt.State] = f
	s.auths.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.sondear(ctx, p, prompt, f)
	}()

	writeJSON(w, http.StatusOK, authStartDTO{State: prompt.State, VerificationURI: prompt.VerificationURI,
		UserCode: prompt.UserCode, ExpiresIn: int(time.Until(prompt.ExpiresAt).Seconds())})
}

func (s *Server) sondear(ctx context.Context, p platforms.Provider, prompt platforms.AuthPrompt, f *authFlow) {
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
		nueva, err := p.PollAuth(ctx, platforms.Credentials{}, prompt)
		switch {
		case err == nil:
			acct, err := s.db.UpsertAccount(ctx, s.cipher, nueva)
			if err != nil {
				s.terminar(f, "error", nil, "no se pudo guardar la cuenta")
				return
			}
			s.db.LogEvent(context.WithoutCancel(ctx), store.Event{Level: store.LevelInfo, Kind: "account_connected",
				Message: "cuenta de " + nombresPlataforma[p.ID()] + " conectada: " + acct.DisplayName})
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
	if f.status == "pending" {
		f.status, f.account, f.message = status, acct, msg
	}
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
	if ok && f.status != "pending" {
		// Un flujo terminado se entrega una vez y se olvida; los expirados sin consultar
		// se limpian de paso.
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
		out = append(out, newAccountDTO(a, dests))
	}
	writeJSON(w, http.StatusOK, out)
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
	if err := s.db.DeleteAccount(r.Context(), id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.db.LogEvent(context.WithoutCancel(r.Context()), store.Event{Level: store.LevelInfo, Kind: "account_disconnected",
		Message: "cuenta de " + nombresPlataforma[platforms.ID(acct.Platform)] + " desconectada: " + acct.DisplayName})
	w.WriteHeader(http.StatusNoContent)
}
