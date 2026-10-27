// Package cookie holds the cookies the API sets and reads: a cashier
// device's token.
package cookie

import (
	"net/http"
	"strings"
)

// A cashier device proves itself with a token only it holds, apart from the
// user signed in on it: the browser keeps it in a cookie that page scripts
// can't read, the Android app in its secure storage, sent in a header. The
// device service issues it at activation.
const (
	// DeviceCookie is the browser's copy, sent with every API request.
	DeviceCookie = "maki_device"
	// DeviceCookiePath covers every API route: the cashier's routes read it.
	DeviceCookiePath = "/api/v1"
	// DeviceHeader carries the Android app's copy.
	DeviceHeader = "X-Device-Token"
	// DeviceCookieMaxAge is 400 days, the longest browsers keep a cookie. The
	// cashier sends the cookie again each time it starts, so a device in use
	// never loses it.
	DeviceCookieMaxAge = 400 * 24 * 60 * 60
)

// NewDeviceCookie is the cookie that gives a browser its device token:
// HttpOnly and SameSite=Strict always, Secure except in development over
// plain HTTP.
func NewDeviceCookie(token string, secure bool) *http.Cookie {
	//nolint:gosec // Secure is off only in development over plain HTTP; HttpOnly and SameSite=Strict always hold
	return &http.Cookie{
		Name:     DeviceCookie,
		Value:    token,
		Path:     DeviceCookiePath,
		MaxAge:   DeviceCookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

// DeviceToken returns the device token the request carries: the header's,
// else the cookie's, else "".
func DeviceToken(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get(DeviceHeader)); token != "" {
		return token
	}
	if cookie, err := r.Cookie(DeviceCookie); err == nil {
		return cookie.Value
	}
	return ""
}
