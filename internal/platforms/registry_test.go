package platforms_test

import (
	"context"
	"testing"

	"github.com/aprendomx/splitstream/internal/crypto"
	"github.com/aprendomx/splitstream/internal/platforms"
	"github.com/aprendomx/splitstream/internal/store"
)

// fakeProvider es el doble mínimo de platforms.Provider: solo ID y Capabilities
// devuelven algo real, el resto son ceros porque el registro no los usa.
type fakeProvider struct {
	id   platforms.ID
	caps platforms.Capabilities
}

func (f *fakeProvider) ID() platforms.ID                     { return f.id }
func (f *fakeProvider) Capabilities() platforms.Capabilities { return f.caps }
func (f *fakeProvider) Configured() bool                     { return false }
func (f *fakeProvider) BeginAuth(context.Context) (platforms.AuthPrompt, error) {
	return platforms.AuthPrompt{}, nil
}
func (f *fakeProvider) PollAuth(context.Context, platforms.AuthPrompt) (store.NewAccount, error) {
	return store.NewAccount{}, nil
}
func (f *fakeProvider) Refresh(context.Context, crypto.Secret) (store.Tokens, error) {
	return store.Tokens{}, nil
}
func (f *fakeProvider) Validate(context.Context, crypto.Secret) (platforms.Identity, error) {
	return platforms.Identity{}, nil
}

func TestRegistryRegistersGetsListsAndReportsCapabilities(t *testing.T) {
	a := &fakeProvider{id: platforms.YouTube, caps: platforms.Capabilities{Title: true}}
	b := &fakeProvider{id: platforms.Twitch, caps: platforms.Capabilities{ChatRead: true}}

	r := platforms.NewRegistry(a, b)

	got, ok := r.Get(platforms.Twitch)
	if !ok || got != platforms.Provider(b) {
		t.Fatalf("Get(twitch) = %v, %v", got, ok)
	}
	if _, ok := r.Get(platforms.Kick); ok {
		t.Error("Get(kick) encontró algo sin haberlo registrado")
	}

	all := r.All()
	if len(all) != 2 || all[0].ID() != platforms.Twitch || all[1].ID() != platforms.YouTube {
		t.Fatalf("All() = %v, quería [twitch youtube]", all)
	}

	caps := r.AllCapabilities()
	if len(caps) != 2 || !caps[platforms.Twitch].ChatRead || !caps[platforms.YouTube].Title {
		t.Errorf("AllCapabilities() = %+v", caps)
	}
}
