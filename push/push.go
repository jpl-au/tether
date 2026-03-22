// Package push sends Web Push notifications to browsers using the Web
// Push protocol (RFC 8291, RFC 8292). It handles VAPID JWT signing and
// payload encryption so the caller only needs a [Sender] and a
// subscription.
//
// Use [GenerateVAPIDKeys] once during initial setup to create a key
// pair. Create a [Sender] with [NewSender] and pass it to
// [tether.PushConfig]. When a subscription arrives, call
// [Sender.Send] to deliver notifications.
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// Notification is the payload sent to the service worker's push event.
// The fields map to the Notification API options that the service
// worker passes to showNotification().
type Notification struct {
	Title    string               `json:"title"`
	Body     string               `json:"body,omitempty"`
	Icon     string               `json:"icon,omitempty"`
	Badge    string               `json:"badge,omitempty"`
	URL      string               `json:"url,omitempty"`
	Tag      string               `json:"tag,omitempty"`      // Groups related notifications; replaces previous with same tag.
	Renotify bool                 `json:"renotify,omitempty"` // Re-alert (vibration/sound) when replacing a tagged notification.
	Silent   bool                 `json:"silent,omitempty"`   // Suppress vibration and sound.
	Actions  []NotificationAction `json:"actions,omitempty"`  // Up to two action buttons shown on the notification.
}

// NotificationAction is a button displayed on a push notification.
// Browsers typically show at most two actions. Each action can navigate
// to its own URL when clicked; if URL is empty, the notification's
// top-level URL is used.
type NotificationAction struct {
	Action string `json:"action"`         // Identifier sent in the notificationclick event.
	Title  string `json:"title"`          // Button label.
	Icon   string `json:"icon,omitempty"` // Optional button icon URL.
	URL    string `json:"url,omitempty"`  // URL to open when this action is clicked.
}

// Config holds application-level VAPID credentials for push delivery.
// Create a [Sender] from a Config to send notifications.
type Config struct {
	// VAPIDPublicKey is the base64url-encoded ECDSA P-256 public key.
	VAPIDPublicKey string

	// VAPIDPrivateKey is the base64url-encoded ECDSA P-256 private key.
	VAPIDPrivateKey string

	// Subject identifies the application server. Must be a mailto: or
	// https: URI (e.g. "mailto:admin@example.com").
	Subject string

	// HTTPClient is the HTTP client used to send push requests. When
	// nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

// Sender delivers Web Push notifications using pre-configured VAPID
// credentials. Create one at startup with [NewSender] and reuse it
// for all push calls.
type Sender struct {
	cfg Config
}

// NewSender creates a Sender from the given configuration.
func NewSender(cfg Config) *Sender {
	return &Sender{cfg: cfg}
}

// PublicKey returns the VAPID public key for client-side push
// subscription. Pass this to [tether.PushConfig] so the browser can
// subscribe to notifications.
func (s *Sender) PublicKey() string {
	return s.cfg.VAPIDPublicKey
}

// Subscription holds the endpoint and encryption keys the browser
// provides after a successful PushManager.subscribe() call. Store this
// server-side to send notifications later via [Send].
type Subscription struct {
	Endpoint string           `json:"endpoint"`
	Keys     SubscriptionKeys `json:"keys"`
}

// SubscriptionKeys holds the client-side ECDH public key and
// authentication secret needed to encrypt push message payloads.
type SubscriptionKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// Validate checks that the subscription has a valid endpoint URL, a
// 65-byte uncompressed P-256 public key, and a 16-byte auth secret.
// Call this before storing a subscription to catch malformed data
// early rather than hitting opaque crypto errors during [Send].
func (s Subscription) Validate() error {
	if s.Endpoint == "" {
		return errors.New("push: empty endpoint")
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil {
		return fmt.Errorf("push: invalid endpoint: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("push: endpoint scheme must be https or http, got %q", u.Scheme)
	}

	pub, err := decodeBase64URL(s.Keys.P256dh)
	if err != nil {
		return fmt.Errorf("push: invalid P-256 public key encoding: %w", err)
	}
	if len(pub) != 65 || pub[0] != 0x04 {
		return errors.New("push: P-256 public key must be 65 bytes (uncompressed)")
	}

	auth, err := decodeBase64URL(s.Keys.Auth)
	if err != nil {
		return fmt.Errorf("push: invalid auth secret encoding: %w", err)
	}
	if len(auth) != 16 {
		return fmt.Errorf("push: auth secret must be 16 bytes, got %d", len(auth))
	}

	return nil
}

// ErrSubscriptionExpired is returned by [Send] when the push service
// responds with HTTP 410 Gone, indicating the subscription is no longer
// valid and should be removed from storage.
var ErrSubscriptionExpired = errors.New("push: subscription expired (410 Gone)")

// Send delivers a push notification to a single subscriber. It encrypts
// the payload, signs the request with a VAPID JWT, and POSTs to the
// push service endpoint. Returns nil on success (HTTP 201).
//
// Returns [ErrSubscriptionExpired] when the push service responds with
// HTTP 410 Gone. Other non-2xx responses are returned as errors with
// the status code in the message.
func (s *Sender) Send(sub Subscription, n Notification) error {
	opts := s.cfg
	payload, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("push: marshal notification: %w", err)
	}

	// Decode the subscriber's P-256 public key and auth secret.
	subPub, err := decodeBase64URL(sub.Keys.P256dh)
	if err != nil {
		return fmt.Errorf("push: decode subscriber public key: %w", err)
	}
	subAuth, err := decodeBase64URL(sub.Keys.Auth)
	if err != nil {
		return fmt.Errorf("push: decode subscriber auth secret: %w", err)
	}

	subKey, err := ecdh.P256().NewPublicKey(subPub)
	if err != nil {
		return fmt.Errorf("push: parse subscriber public key: %w", err)
	}

	// Generate an ephemeral ECDH key pair for this message.
	ephPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("push: generate ephemeral key: %w", err)
	}
	ephPub := ephPriv.PublicKey().Bytes()

	// ECDH key agreement.
	shared, err := ephPriv.ECDH(subKey)
	if err != nil {
		return fmt.Errorf("push: ECDH key agreement: %w", err)
	}

	// Generate 16-byte salt for content encryption.
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("push: generate salt: %w", err)
	}

	// RFC 8291 Section 3.4: derive content encryption key and nonce.
	cek, nonce, err := deriveKeyAndNonce(shared, subAuth, subPub, ephPub, salt)
	if err != nil {
		return fmt.Errorf("push: derive encryption keys: %w", err)
	}

	// Encrypt the payload with AES-128-GCM.
	encrypted, err := encryptPayload(cek, nonce, payload)
	if err != nil {
		return fmt.Errorf("push: encrypt payload: %w", err)
	}

	// Build the aes128gcm content body (RFC 8188).
	body := buildAES128GCMBody(salt, ephPub, encrypted)

	// Create the VAPID Authorisation header.
	authHeader, err := vapidAuth(sub.Endpoint, opts)
	if err != nil {
		return fmt.Errorf("push: create VAPID auth: %w", err)
	}

	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequest("POST", sub.Endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("push: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("TTL", "86400")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("push: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		return ErrSubscriptionExpired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("push: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// GenerateVAPIDKeys generates a new ECDSA P-256 key pair for VAPID
// authentication. Returns base64url-encoded public and private keys.
// Call this once during initial setup and store the keys securely.
func GenerateVAPIDKeys() (publicKey, privateKey string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("push: generate VAPID keys: %w", err)
	}

	// Convert to ECDH for clean byte extraction without using
	// deprecated struct fields.
	ecdhKey, err := key.ECDH()
	if err != nil {
		return "", "", fmt.Errorf("push: convert VAPID key: %w", err)
	}

	publicKey = base64.RawURLEncoding.EncodeToString(ecdhKey.PublicKey().Bytes())
	privateKey = base64.RawURLEncoding.EncodeToString(ecdhKey.Bytes())
	return publicKey, privateKey, nil
}

// --- Internal helpers ---

func decodeBase64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// deriveKeyAndNonce derives the content encryption key (16 bytes) and
// nonce (12 bytes) per RFC 8291 Section 3.4, using golang.org/x/crypto/hkdf.
func deriveKeyAndNonce(sharedSecret, authSecret, subPub, ephPub, salt []byte) (cek, nonce []byte, err error) {
	// Step 1: derive input keying material from the ECDH shared secret.
	//   info = "WebPush: info" || 0x00 || ua_public || as_public
	keyInfo := buildInfo("WebPush: info", subPub, ephPub)
	ikm := make([]byte, 32)
	r := hkdf.New(sha256.New, sharedSecret, authSecret, keyInfo)
	if _, err := io.ReadFull(r, ikm); err != nil {
		return nil, nil, fmt.Errorf("derive IKM: %w", err)
	}

	// Step 2: derive CEK and nonce from the salt and IKM.
	cek = make([]byte, 16)
	r = hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: aes128gcm\x00"))
	if _, err := io.ReadFull(r, cek); err != nil {
		return nil, nil, fmt.Errorf("derive CEK: %w", err)
	}

	nonce = make([]byte, 12)
	r = hkdf.New(sha256.New, ikm, salt, []byte("Content-Encoding: nonce\x00"))
	if _, err := io.ReadFull(r, nonce); err != nil {
		return nil, nil, fmt.Errorf("derive nonce: %w", err)
	}

	return cek, nonce, nil
}

// buildInfo creates the info parameter for RFC 8291 key derivation.
// Format: label || 0x00 || recipient_public || sender_public
func buildInfo(label string, recipientPub, senderPub []byte) []byte {
	info := make([]byte, 0, len(label)+1+len(recipientPub)+len(senderPub))
	info = append(info, label...)
	info = append(info, 0x00)
	info = append(info, recipientPub...)
	info = append(info, senderPub...)
	return info
}

// encryptPayload encrypts data using AES-128-GCM with a 0x02 delimiter
// byte per RFC 8188 Section 2.
func encryptPayload(key, nonce, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Append the delimiter byte (0x02 = final record) so the receiver
	// can distinguish content from padding.
	padded := append(plaintext, 0x02)

	return gcm.Seal(nil, nonce, padded, nil), nil
}

// buildAES128GCMBody builds the encrypted content body per RFC 8188.
// Format: salt (16) || rs (4) || idlen (1) || keyid (65) || ciphertext
func buildAES128GCMBody(salt, keyID, ciphertext []byte) []byte {
	rs := uint32(4096) // record size

	buf := make([]byte, 0, 16+4+1+len(keyID)+len(ciphertext))
	buf = append(buf, salt...)

	var rsBuf [4]byte
	binary.BigEndian.PutUint32(rsBuf[:], rs)
	buf = append(buf, rsBuf[:]...)

	buf = append(buf, byte(len(keyID)))
	buf = append(buf, keyID...)
	buf = append(buf, ciphertext...)

	return buf
}

// vapidAuth creates the VAPID Authorisation header value per RFC 8292.
func vapidAuth(endpoint string, opts Config) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint: %w", err)
	}
	audience := u.Scheme + "://" + u.Host

	// Reconstruct the ECDSA signing key from the raw private key bytes.
	privBytes, err := decodeBase64URL(opts.VAPIDPrivateKey)
	if err != nil {
		return "", fmt.Errorf("decode VAPID private key: %w", err)
	}
	privKey, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), privBytes)
	if err != nil {
		return "", fmt.Errorf("parse VAPID private key: %w", err)
	}

	// Build the JWT header and claims.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))

	exp := time.Now().Add(12 * time.Hour).Unix()
	claimsJSON, err := json.Marshal(map[string]any{
		"aud": audience,
		"exp": exp,
		"sub": opts.Subject,
	})
	if err != nil {
		return "", fmt.Errorf("marshal JWT claims: %w", err)
	}
	claims := base64.RawURLEncoding.EncodeToString(claimsJSON)

	// Sign with ES256 (ECDSA P-256 + SHA-256).
	signingInput := header + "." + claims
	hash := sha256.Sum256([]byte(signingInput))

	r, s, err := ecdsa.Sign(rand.Reader, privKey, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	// Encode r and s as 32-byte big-endian, concatenated (64 bytes)
	// per JWS ES256 (RFC 7518 Section 3.4).
	sig := make([]byte, 64)
	rBytes := r.Bytes()
	sBytes := s.Bytes()
	copy(sig[32-len(rBytes):32], rBytes)
	copy(sig[64-len(sBytes):], sBytes)

	token := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	return fmt.Sprintf("vapid t=%s,k=%s", token, opts.VAPIDPublicKey), nil
}
