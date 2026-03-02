package tether

import "errors"

// Sentinel errors returned by push-related methods.
var (
	// ErrPushNotConfigured is returned by [Session.Push] when the
	// handler was created without push support (no PushConfig).
	ErrPushNotConfigured = errors.New("tether: push not configured")

	// ErrPushNoSubscription is returned by [Session.Push] when the
	// browser has not yet registered a push subscription.
	ErrPushNoSubscription = errors.New("tether: no push subscription for session")

	// ErrPushPreWarm is returned by [captureSession.Push] during
	// pre-warming because no browser subscription exists yet.
	ErrPushPreWarm = errors.New("tether: push not available during pre-warming")
)
