package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/LupovichRan/AutoDev_Bank/internal/testutil"
)

func TestAuditSignerFor(t *testing.T) {
	seed := strings.Repeat("ab", ed25519.SeedSize)
	seedBytes, _ := hex.DecodeString(seed)
	want := ed25519.NewKeyFromSeed(seedBytes)

	for _, origin := range []string{"http://localhost:8080", "http://LOCALHOST", "https://bank.localhost",
		"http://127.0.0.1:8080", "http://127.5.6.7", "http://[::1]:8080"} {
		key, err := auditSignerFor(Config{Origin: origin})
		testutil.NoError(t, err, origin)
		testutil.Len(t, key, ed25519.PrivateKeySize, origin)
	}
	// Two ephemeral keys differ.
	a, _ := auditSignerFor(Config{Origin: "http://localhost"})
	b, _ := auditSignerFor(Config{Origin: "http://localhost"})
	testutil.False(t, a.Equal(b), "ephemeral keys are random")

	for _, origin := range []string{"https://bank.example", "http://10.0.0.5", "http://localhost.example.com",
		"http://notlocalhost", "", "not a url"} {
		_, err := auditSignerFor(Config{Origin: origin})
		testutil.ErrorContains(t, err, "BANK_AUDIT_SIGNING_KEY", origin)
	}

	for _, origin := range []string{"https://bank.example", "http://localhost"} {
		key, err := auditSignerFor(Config{Origin: origin, AuditSigningKey: seed})
		testutil.NoError(t, err, origin)
		testutil.True(t, want.Equal(key), "key from seed")
	}

	for _, bad := range []string{"zz", strings.Repeat("g", 64), strings.Repeat("ab", 31)} {
		_, err := auditSignerFor(Config{Origin: "http://localhost", AuditSigningKey: bad})
		testutil.ErrorContains(t, err, "64 hex", bad)
	}
}
