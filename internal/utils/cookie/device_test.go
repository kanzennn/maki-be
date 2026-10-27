package cookie_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kervzent/maki-be/internal/utils/cookie"
)

func TestDeviceToken(t *testing.T) {
	request := func(header, value string) *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/outlets/o1/pos/device", nil)
		if header != "" {
			r.Header.Set(cookie.DeviceHeader, header)
		}
		if value != "" {
			// A request's cookie has only its name and value; the attributes
			// are the Set-Cookie's.
			r.AddCookie(&http.Cookie{Name: cookie.DeviceCookie, Value: value}) //nolint:gosec // as above
		}
		return r
	}
	for _, tt := range []struct {
		name, header, cookie, want string
	}{
		{"the app's header", "app-token", "", "app-token"},
		{"the browser's cookie", "", "browser-token", "browser-token"},
		{"the header first", "app-token", "browser-token", "app-token"},
		{"a blank header falls back to the cookie", "  ", "browser-token", "browser-token"},
		{"neither", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := cookie.DeviceToken(request(tt.header, tt.cookie)); got != tt.want {
				t.Errorf("cookie.DeviceToken = %q, want %q", got, tt.want)
			}
		})
	}
}
