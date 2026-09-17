package store

import "testing"

func TestSecretSealRoundTripAndNoPlaintext(t *testing.T) {
	t.Setenv("SHIPYARD_SECRET_KEY", "test-key")
	ciphertext, nonce, err := sealSecret("do-not-log")
	if err != nil {
		t.Fatal(err)
	}
	if string(ciphertext) == "do-not-log" {
		t.Fatal("secret was stored as plaintext")
	}
	got, err := openSecret(ciphertext, nonce)
	if err != nil || got != "do-not-log" {
		t.Fatalf("round trip failed: %q %v", got, err)
	}
}

func TestSecretKeyIsRequired(t *testing.T) {
	t.Setenv("SHIPYARD_SECRET_KEY", "")
	if _, _, err := sealSecret("value"); err != ErrSecretKeyMissing {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestSafeEnvironmentName(t *testing.T) {
	for _, value := range []string{"TOKEN", "A_2"} {
		if !isSafeEnvName(value) {
			t.Errorf("%q rejected", value)
		}
	}
	for _, value := range []string{"", "token", "A-B"} {
		if isSafeEnvName(value) {
			t.Errorf("%q accepted", value)
		}
	}
}

func TestSafeSecretName(t *testing.T) {
	for _, value := range []string{"OPENAI", "prod-api_key", "A1"} {
		if !isSafeSecretName(value) {
			t.Errorf("%q rejected", value)
		}
	}
	for _, value := range []string{"", "prod api", "token/1"} {
		if isSafeSecretName(value) {
			t.Errorf("%q accepted", value)
		}
	}
}
