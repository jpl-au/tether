package tether

import "net/http"

// csrf validates TrustedOrigins and creates a CrossOriginProtection.
// Panics on empty strings or invalid origins so configuration errors
// surface at startup, not at runtime.
func (s *Security) csrf() *http.CrossOriginProtection {
	for _, o := range s.TrustedOrigins {
		if o == "" {
			panic("tether: Security.TrustedOrigins contains an empty string — remove it or provide a valid origin like \"https://example.com\"")
		}
	}
	csrf := http.NewCrossOriginProtection()
	for _, origin := range s.TrustedOrigins {
		if err := csrf.AddTrustedOrigin(origin); err != nil {
			panic("tether: invalid TrustedOrigins entry " + origin + ": " + err.Error())
		}
	}
	return csrf
}
