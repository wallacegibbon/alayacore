package auth

import (
	"testing"
)

func TestLookupDefaultClient_GitHub(t *testing.T) {
	id, secret, ok := LookupDefaultClient("https://github.com")
	if !ok {
		t.Fatal("expected GitHub default client")
	}
	if id != "Ov23lipCk4st2cXDZixb" {
		t.Errorf("got client_id %q, want Ov23lipCk4st2cXDZixb", id)
	}
	if secret != "ae1f4f4238cb27683cc22b7f40543de1c6d67b08" {
		t.Errorf("got client_secret %q, want ae1f4f4...", secret)
	}
}

func TestLookupDefaultClient_Unknown(t *testing.T) {
	id, secret, ok := LookupDefaultClient("https://unknown.example.com")
	if ok {
		t.Errorf("expected not found, got client_id %q secret %q", id, secret)
	}
	if id != "" {
		t.Errorf("expected empty client_id, got %q", id)
	}
	if secret != "" {
		t.Errorf("expected empty secret, got %q", secret)
	}
}

func TestLookupDefaultClient_PrefixMatch(t *testing.T) {
	id, _, ok := LookupDefaultClient("https://github.com/login/oauth")
	if !ok {
		t.Fatal("expected prefix match")
	}
	if id != "Ov23lipCk4st2cXDZixb" {
		t.Errorf("got client_id %q", id)
	}
}

func TestRegisterDefaultClient(t *testing.T) {
	RegisterDefaultClient("https://gitlab.com", "gitlab-client-123", "gitlab-secret")

	id, secret, ok := LookupDefaultClient("https://gitlab.com")
	if !ok {
		t.Fatal("expected gitlab default client after register")
	}
	if id != "gitlab-client-123" {
		t.Errorf("got client_id %q", id)
	}
	if secret != "gitlab-secret" {
		t.Errorf("got secret %q", secret)
	}
}
