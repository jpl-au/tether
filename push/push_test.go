package push

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateVAPIDKeys(t *testing.T) {
	pub, priv, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys: %v", err)
	}

	// Public key should be 65 bytes (uncompressed P-256 point).
	pubBytes, err := decodeBase64URL(pub)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	if len(pubBytes) != 65 {
		t.Errorf("public key length = %d, want 65", len(pubBytes))
	}
	if pubBytes[0] != 0x04 {
		t.Errorf("public key prefix = 0x%02x, want 0x04 (uncompressed)", pubBytes[0])
	}

	// Private key should be 32 bytes (P-256 scalar).
	privBytes, err := decodeBase64URL(priv)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	if len(privBytes) != 32 {
		t.Errorf("private key length = %d, want 32", len(privBytes))
	}

	// Keys should be usable: round-trip through ParseRawPrivateKey.
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), privBytes)
	if err != nil {
		t.Fatalf("ParseRawPrivateKey: %v", err)
	}

	// Verify the public key matches.
	ecdhKey, err := key.ECDH()
	if err != nil {
		t.Fatalf("ECDH conversion: %v", err)
	}
	got := base64.RawURLEncoding.EncodeToString(ecdhKey.PublicKey().Bytes())
	if got != pub {
		t.Errorf("public key mismatch:\n  got  %s\n  want %s", got, pub)
	}
}

func TestGenerateVAPIDKeysUnique(t *testing.T) {
	pub1, priv1, _ := GenerateVAPIDKeys()
	pub2, priv2, _ := GenerateVAPIDKeys()

	if pub1 == pub2 {
		t.Error("two generated public keys should be different")
	}
	if priv1 == priv2 {
		t.Error("two generated private keys should be different")
	}
}

func TestVAPIDAuth(t *testing.T) {
	pub, priv, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys: %v", err)
	}

	opts := Options{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		Subject:         "mailto:test@example.com",
	}

	header, err := vapidAuth("https://push.example.com/v1/send", opts)
	if err != nil {
		t.Fatalf("vapidAuth: %v", err)
	}

	// Header format: "vapid t=<JWT>,k=<public key>"
	if len(header) < 10 {
		t.Fatalf("header too short: %q", header)
	}
	if header[:8] != "vapid t=" {
		t.Errorf("header should start with 'vapid t=', got %q", header[:8])
	}

	// Verify the JWT signature. Extract the three parts.
	// Format: vapid t=header.claims.sig,k=pubkey
	rest := header[8:]
	commaIdx := -1
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == ',' {
			commaIdx = i
			break
		}
	}
	if commaIdx < 0 {
		t.Fatalf("no comma in header: %q", header)
	}
	token := rest[:commaIdx]

	parts := splitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("JWT should have 3 parts, got %d", len(parts))
	}

	// Verify the signature.
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(sigBytes) != 64 {
		t.Fatalf("signature length = %d, want 64", len(sigBytes))
	}

	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))

	pubBytes, _ := decodeBase64URL(pub)
	ecdhPub, _ := ecdh.P256().NewPublicKey(pubBytes)
	ecdsaPub, _ := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), ecdhPub.Bytes())

	if !ecdsa.Verify(ecdsaPub, hash[:], bytesToBigInt(sigBytes[:32]), bytesToBigInt(sigBytes[32:])) {
		t.Error("JWT signature verification failed")
	}

	// Verify the claims contain the correct audience.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims["aud"] != "https://push.example.com" {
		t.Errorf("audience = %v, want %q", claims["aud"], "https://push.example.com")
	}
	if claims["sub"] != "mailto:test@example.com" {
		t.Errorf("subject = %v, want %q", claims["sub"], "mailto:test@example.com")
	}
}

func TestSendSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("Content-Encoding = %q, want %q", r.Header.Get("Content-Encoding"), "aes128gcm")
		}
		if r.Header.Get("TTL") != "86400" {
			t.Errorf("TTL = %q, want %q", r.Header.Get("TTL"), "86400")
		}
		auth := r.Header.Get("Authorization")
		if len(auth) < 10 || auth[:8] != "vapid t=" {
			t.Errorf("Authorization header malformed: %q", auth)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sub := newTestSubscription(t, srv.URL)

	pub, priv, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys: %v", err)
	}

	err = Send(sub, Notification{Title: "Test", Body: "Hello"}, Options{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		Subject:         "mailto:test@example.com",
		HTTPClient:      srv.Client(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSendExpiredSubscription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	sub := newTestSubscription(t, srv.URL)
	pub, priv, _ := GenerateVAPIDKeys()

	err := Send(sub, Notification{Title: "Test"}, Options{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		Subject:         "mailto:test@example.com",
		HTTPClient:      srv.Client(),
	})
	if err != ErrSubscriptionExpired {
		t.Fatalf("Send = %v, want ErrSubscriptionExpired", err)
	}
}

func TestSendServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sub := newTestSubscription(t, srv.URL)
	pub, priv, _ := GenerateVAPIDKeys()

	err := Send(sub, Notification{Title: "Test"}, Options{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		Subject:         "mailto:test@example.com",
		HTTPClient:      srv.Client(),
	})
	if err == nil {
		t.Fatal("Send should return error for 500")
	}
}

func TestNotificationSilentField(t *testing.T) {
	n := Notification{Title: "Quiet", Silent: true}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	if m["silent"] != true {
		t.Errorf("silent = %v, want true", m["silent"])
	}
}

func TestNotificationSilentOmitted(t *testing.T) {
	n := Notification{Title: "Loud"}
	data, _ := json.Marshal(n)
	var m map[string]any
	json.Unmarshal(data, &m)
	if _, ok := m["silent"]; ok {
		t.Error("silent should be omitted when false")
	}
}

func TestNotificationActions(t *testing.T) {
	n := Notification{
		Title: "Message",
		URL:   "/inbox",
		Actions: []NotificationAction{
			{Action: "reply", Title: "Reply", URL: "/inbox?reply=1"},
			{Action: "dismiss", Title: "Dismiss"},
		},
	}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)

	actions, ok := m["actions"].([]any)
	if !ok {
		t.Fatalf("actions not an array: %T", m["actions"])
	}
	if len(actions) != 2 {
		t.Fatalf("len(actions) = %d, want 2", len(actions))
	}

	first := actions[0].(map[string]any)
	if first["action"] != "reply" {
		t.Errorf("first action = %v, want %q", first["action"], "reply")
	}
	if first["url"] != "/inbox?reply=1" {
		t.Errorf("first url = %v, want %q", first["url"], "/inbox?reply=1")
	}

	second := actions[1].(map[string]any)
	if _, ok := second["url"]; ok {
		t.Error("second action should omit empty url")
	}
}

func TestNotificationActionsOmitted(t *testing.T) {
	n := Notification{Title: "Simple"}
	data, _ := json.Marshal(n)
	var m map[string]any
	json.Unmarshal(data, &m)
	if _, ok := m["actions"]; ok {
		t.Error("actions should be omitted when nil")
	}
}

func TestSendWithActions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sub := newTestSubscription(t, srv.URL)
	pub, priv, _ := GenerateVAPIDKeys()

	err := Send(sub, Notification{
		Title:  "Chat",
		Body:   "New message",
		Silent: true,
		Actions: []NotificationAction{
			{Action: "reply", Title: "Reply", URL: "/chat?reply=1"},
		},
	}, Options{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		Subject:         "mailto:test@example.com",
		HTTPClient:      srv.Client(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// --- Test helpers ---

// newTestSubscription creates a Subscription with a real P-256 key pair
// so the encryption pipeline runs end-to-end.
func newTestSubscription(t *testing.T, endpoint string) Subscription {
	t.Helper()

	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate subscriber key: %v", err)
	}

	auth := make([]byte, 16)

	return Subscription{
		Endpoint: endpoint,
		Keys: SubscriptionKeys{
			P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
			Auth:   base64.RawURLEncoding.EncodeToString(auth),
		},
	}
}

// splitN splits s by sep into at most n parts.
func splitN(s, sep string, n int) []string {
	var result []string
	for i := 0; i < n-1; i++ {
		idx := indexOf(s, sep)
		if idx < 0 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	result = append(result, s)
	return result
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// bytesToBigInt converts a big-endian byte slice to *big.Int for
// ECDSA signature verification.
func bytesToBigInt(b []byte) *big.Int {
	return new(big.Int).SetBytes(b)
}
