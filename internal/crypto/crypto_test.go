package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"testing"
)

func TestStudentAliasVector(t *testing.T) {
	const want = "7ca34e3c9fb519103feabf9c0e1954112556e0be0ed2656a731ea318f0cafbe3"
	if got := StudentAlias("2023000001"); got != want {
		t.Fatalf("alias = %s, want %s", got, want)
	}
}

func TestEncryptAndArgon2(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	ciphertext, err := Encrypt(key, "sensitive-value")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := Decrypt(key, ciphertext)
	if err != nil || plaintext != "sensitive-value" {
		t.Fatalf("decrypt = %q, %v", plaintext, err)
	}
	hash, err := Argon2idHash("password")
	if err != nil || !Argon2idVerify(hash, "password") || Argon2idVerify(hash, "wrong") {
		t.Fatal("argon2 verification failed")
	}
}

func TestP256Signature(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(der)
	parsed, err := ParseP256PublicKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("canonical-message")
	sum := sha256.Sum256(message)
	signature, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyP256Signature(parsed, message, base64.RawURLEncoding.EncodeToString(signature)) {
		t.Fatal("signature did not verify")
	}
}
