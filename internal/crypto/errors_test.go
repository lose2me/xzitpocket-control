package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
)

func TestDecryptRejectsBadInput(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	if _, err := Decrypt(key[:16], "x"); err == nil {
		t.Fatal("short key should fail")
	}
	if _, err := Encrypt(key[:16], "x"); err == nil {
		t.Fatal("short encryption key should fail")
	}
	if _, err := Decrypt(key, "!!not-base64!!"); err == nil {
		t.Fatal("invalid base64 should fail")
	}
	if _, err := Decrypt(key, base64.RawURLEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("short ciphertext should fail")
	}
	ciphertext, err := Encrypt(key, "value")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(ciphertext)
	raw[len(raw)-1] ^= 0xff
	if _, err := Decrypt(key, base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered ciphertext should fail")
	}
	otherKey := []byte("abcdefghijklmnopqrstuvwxyz012345")
	if _, err := Decrypt(otherKey, ciphertext); err == nil {
		t.Fatal("wrong key should fail")
	}
}

func TestValidateStudentID(t *testing.T) {
	for _, ok := range []string{"2023000001", "a.b_c-d", strings.Repeat("a", 64)} {
		if err := ValidateStudentID(ok); err != nil {
			t.Fatalf("ValidateStudentID(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "学号", "has space", strings.Repeat("a", 65), "a/b"} {
		if err := ValidateStudentID(bad); err == nil {
			t.Fatalf("ValidateStudentID(%q) should fail", bad)
		}
	}
}

func TestParseP256PublicKeyErrors(t *testing.T) {
	if _, err := ParseP256PublicKey("!!!"); err == nil {
		t.Fatal("invalid base64 should fail")
	}
	if _, err := ParseP256PublicKey(base64.RawURLEncoding.EncodeToString([]byte("nope"))); err == nil {
		t.Fatal("invalid DER should fail")
	}
	rsaKey, err := rsaPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseP256PublicKey(base64.RawURLEncoding.EncodeToString(rsaKey)); err == nil || !strings.Contains(err.Error(), "P-256") {
		t.Fatalf("non-ECDSA key error = %v", err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&p384.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseP256PublicKey(base64.RawURLEncoding.EncodeToString(der)); err == nil {
		t.Fatal("P-384 key should fail")
	}
}

func TestVerifyP256SignatureRejectsMalformed(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyP256Signature(&key.PublicKey, []byte("msg"), "!!!") {
		t.Fatal("invalid base64 signature should not verify")
	}
	if VerifyP256Signature(&key.PublicKey, []byte("msg"), base64.RawURLEncoding.EncodeToString([]byte("short"))) {
		t.Fatal("truncated signature should not verify")
	}
}

func TestCanonicalLines(t *testing.T) {
	joined, err := CanonicalLines("a", "b", "")
	if err != nil || string(joined) != "a\nb\n" {
		t.Fatalf("CanonicalLines = %q, %v", joined, err)
	}
	for _, bad := range []string{"a\rb", "a\nb", "a\x00b"} {
		if _, err := CanonicalLines("ok", bad); err == nil {
			t.Fatalf("CanonicalLines(%q) should fail", bad)
		}
	}
}

func TestArgon2idVerifyRejectsMalformedHashes(t *testing.T) {
	hash, err := Argon2idHash("secret")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(hash, "$")
	cases := []string{
		"",
		"$argon2id$v=19$m=1,t=1,p=1$only-five",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		strings.Replace(hash, "v=19", "v=16", 1),
		strings.Replace(hash, parts[3], "bad", 1),
		strings.Replace(hash, parts[4], "!!!", 1),
		strings.Replace(hash, parts[5], "", 1),
		strings.Replace(hash, parts[5], "!!!", 1),
	}
	for _, encoded := range cases {
		if Argon2idVerify(encoded, "secret") {
			t.Fatalf("Argon2idVerify(%q) should fail", encoded)
		}
	}
	weak := "$argon2id$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA"
	if Argon2idVerify(weak, "secret") {
		t.Fatal("mismatched hash should fail")
	}
}

func TestJSONFallsBackOnUnsupportedValues(t *testing.T) {
	if got := JSON(map[string]any{"a": 1}); got != `{"a":1}` {
		t.Fatalf("JSON = %q", got)
	}
	if got := JSON(func() {}); got != "{}" {
		t.Fatalf("JSON unsupported = %q", got)
	}
}

func TestHMACHelpers(t *testing.T) {
	if HashToken([]byte("key"), "value") != HMACHex([]byte("key"), "value") {
		t.Fatal("HashToken and HMACHex must agree")
	}
	if HashToken([]byte("key"), "value") == HashToken([]byte("key"), "other") {
		t.Fatal("different values must hash differently")
	}
	token, err := NewToken(16)
	if err != nil || len(token) == 0 {
		t.Fatalf("NewToken = %q, %v", token, err)
	}
	id, err := NewID("dev")
	if err != nil || !strings.HasPrefix(id, "dev_") {
		t.Fatalf("NewID = %q, %v", id, err)
	}
}

func rsaPublicKey() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	return x509.MarshalPKIXPublicKey(&key.PublicKey)
}
