package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/store"
)

func cifradorDePrueba(t *testing.T) *crypto.Cipher {
	t.Helper()
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 7)
	}
	c, err := crypto.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cuentaDePrueba(t *testing.T, db *store.DB, c *crypto.Cipher, ext string) *store.Account {
	t.Helper()
	a, err := db.UpsertAccount(context.Background(), c, store.NewAccount{
		Platform: store.PlatformTwitch, ExternalID: ext, DisplayName: "canal_" + ext,
		Scopes: []string{"channel:manage:broadcast", "user:read:chat"},
		Tokens: store.Tokens{Access: crypto.Secret("acceso-" + ext), Refresh: crypto.Secret("refresco-" + ext),
			ExpiresAt: time.Now().Add(4 * time.Hour)},
	})
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	return a
}

func TestAccountNeverCarriesATokenField(t *testing.T) {
	rt := reflect.TypeOf(store.Account{})
	for i := 0; i < rt.NumField(); i++ {
		if strings.Contains(strings.ToLower(rt.Field(i).Name), "token") {
			t.Errorf("Account.%s: serializar una cuenta no puede filtrar tokens", rt.Field(i).Name)
		}
	}
	b, _ := json.Marshal(store.Account{DisplayName: "x"})
	if strings.Contains(string(b), "acceso") || strings.Contains(strings.ToLower(string(b)), "token") {
		t.Errorf("el JSON de Account menciona tokens: %s", b)
	}
}

func TestUpsertAccountEncryptsAndReturnsTokensOnlyThroughAccountTokens(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	a := cuentaDePrueba(t, db, c, "42")
	if a.ID == 0 || a.Platform != store.PlatformTwitch || a.DisplayName != "canal_42" || a.Status != store.AccountStatusOK {
		t.Fatalf("cuenta = %+v", a)
	}
	if len(a.Scopes) != 2 || a.ExpiresAt == nil {
		t.Errorf("scopes = %v, expires = %v", a.Scopes, a.ExpiresAt)
	}

	tok, err := db.AccountTokens(context.Background(), c, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access.Reveal() != "acceso-42" || tok.Refresh.Reveal() != "refresco-42" {
		t.Errorf("tokens descifrados mal: %+v", tok)
	}
	// En la base no está en claro.
	var blob []byte
	if err := db.SQL().QueryRowContext(context.Background(),
		`SELECT access_token_encrypted FROM platform_accounts WHERE id = ?`, a.ID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "acceso-42") {
		t.Error("el token está en claro en la base")
	}
}

func TestUpsertAccountUpdatesTheSameUserAndResetsReauth(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "42")
	if err := db.SetAccountStatus(ctx, a.ID, store.AccountStatusReauth); err != nil {
		t.Fatal(err)
	}
	b, err := db.UpsertAccount(ctx, c, store.NewAccount{
		Platform: store.PlatformTwitch, ExternalID: "42", DisplayName: "canal_nuevo",
		Scopes: []string{"user:read:chat"},
		Tokens: store.Tokens{Access: "acceso-2", Refresh: "refresco-2", ExpiresAt: time.Now().Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.ID != a.ID || b.DisplayName != "canal_nuevo" || b.Status != store.AccountStatusOK {
		t.Errorf("upsert = %+v, quería el mismo id, nombre nuevo y status ok", b)
	}
	tok, _ := db.AccountTokens(ctx, c, a.ID)
	if tok.Access.Reveal() != "acceso-2" {
		t.Errorf("access = %s, quería el nuevo", tok.Access.Reveal())
	}
	todas, _ := db.Accounts(ctx)
	if len(todas) != 1 {
		t.Errorf("cuentas = %d, quería 1", len(todas))
	}
}

func TestSaveTokensRotatesTheRefreshToken(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "42")
	exp := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	if err := db.SaveTokens(ctx, c, a.ID, store.Tokens{Access: "a2", Refresh: "r2", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	tok, _ := db.AccountTokens(ctx, c, a.ID)
	if tok.Access.Reveal() != "a2" || tok.Refresh.Reveal() != "r2" || !tok.ExpiresAt.Equal(exp) {
		t.Errorf("tokens = %+v", tok)
	}
	if err := db.SaveTokens(ctx, c, 999, store.Tokens{Access: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cuenta inexistente: err = %v", err)
	}
}

func TestLinkDestinationRequiresMatchingPlatformAndCascades(t *testing.T) {
	db := openTemp(t)
	c := cifradorDePrueba(t)
	ctx := context.Background()
	a := cuentaDePrueba(t, db, c, "42")
	tw, err := db.CreateDestination(ctx, c, store.NewDestination{Name: "Twitch", Platform: store.PlatformTwitch, RTMPURL: "rtmp://live.twitch.tv/app", Key: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	yt, err := db.CreateDestination(ctx, c, store.NewDestination{Name: "YouTube", Platform: store.PlatformYouTube, RTMPURL: "rtmp://a.rtmp.youtube.com/live2", Key: "k", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.LinkDestination(ctx, yt.ID, a.ID); !errors.Is(err, store.ErrInvalidInput) {
		t.Errorf("plataformas distintas: err = %v, quería ErrInvalidInput", err)
	}
	if err := db.LinkDestination(ctx, tw.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	d, _ := db.DestinationByID(ctx, tw.ID)
	if d.AccountID == nil || *d.AccountID != a.ID {
		t.Errorf("Destination.AccountID = %v, quería %d", d.AccountID, a.ID)
	}
	got, err := db.AccountForDestination(ctx, tw.ID)
	if err != nil || got.ID != a.ID {
		t.Errorf("AccountForDestination = %v, %v", got, err)
	}
	if _, err := db.AccountForDestination(ctx, yt.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("sin cuenta: err = %v", err)
	}
	ids, _ := db.DestinationsOfAccount(ctx, a.ID)
	if len(ids) != 1 || ids[0] != tw.ID {
		t.Errorf("DestinationsOfAccount = %v", ids)
	}

	// Vincular otra vez sustituye (PRIMARY KEY por destino).
	b := cuentaDePrueba(t, db, c, "43")
	if err := db.LinkDestination(ctx, tw.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = db.DestinationByID(ctx, tw.ID)
	if *d.AccountID != b.ID {
		t.Errorf("AccountID = %d tras revincular, quería %d", *d.AccountID, b.ID)
	}
	if err := db.UnlinkDestination(ctx, tw.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = db.DestinationByID(ctx, tw.ID)
	if d.AccountID != nil {
		t.Error("tras Unlink el destino sigue con cuenta")
	}

	// Borrar la cuenta desvincula; borrar el destino también.
	if err := db.LinkDestination(ctx, tw.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	d, _ = db.DestinationByID(ctx, tw.ID)
	if d.AccountID != nil {
		t.Error("tras borrar la cuenta el destino sigue enlazado")
	}
	if err := db.DeleteAccount(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("segundo borrado: err = %v", err)
	}
	if err := db.LinkDestination(ctx, tw.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteDestination(ctx, tw.ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := db.DestinationsOfAccount(ctx, b.ID); len(ids) != 0 {
		t.Errorf("tras borrar el destino quedan enlaces: %v", ids)
	}
}
