package tether

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReconnectTicketNavigation(t *testing.T) {
	for _, path := range []string{"/items?q=two", "", "relative", "https://example.com/", "//example.com/", `/\example.com/`} {
		t.Run(path, func(t *testing.T) {
			h := newReattachHandler()
			h.csrf = &http.CrossOriginProtection{}
			r := httptest.NewRequest("POST", "/?tether=ticket", nil)
			r.Header.Set("Tether-Navigate", path)
			w := httptest.NewRecorder()
			h.handleConnectTicket(w, r)
			valid := path == "" || path == "/items?q=two"
			if !valid {
				if w.Code != http.StatusBadRequest {
					t.Errorf("invalid browser navigation accepted: %q status=%d", path, w.Code)
				}
				return
			}
			if w.Code != http.StatusOK {
				t.Fatalf("ticket failed: %d", w.Code)
			}
			ticket, ok := h.redeemTicket(w.Body.String(), "")
			if !ok || ticket.navigate != path {
				t.Errorf("ticket lost navigation: %+v, valid=%t", ticket, ok)
			}
		})
	}
}
