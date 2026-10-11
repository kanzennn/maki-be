package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type loginBody struct {
	Username string           `json:"username" binding:"required"`
	Password string           `json:"password" binding:"required,max=128"`
	Client   string           `json:"client" binding:"required,oneof=web mobile"`
	Locale   Nullable[string] `json:"locale"`
}

func bind(t *testing.T, body string) (loginBody, error) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	var dst loginBody
	return dst, BindJSON(c, &dst)
}

func TestBindJSONAcceptsAValidBody(t *testing.T) {
	got, err := bind(t, `{"username":"kasir1","password":"kopi susu","client":"web"}`)
	if err != nil {
		t.Fatalf("BindJSON: %v", err)
	}
	if got.Username != "kasir1" || got.Client != "web" {
		t.Errorf("got %+v", got)
	}
}

func TestBindJSONRejects(t *testing.T) {
	tests := []struct {
		name, body string
		code       string
		fields     map[string]string
	}{
		{"not JSON", `username=kasir1`, "invalid_request", nil},
		{"empty body", ``, "invalid_request", nil},
		{"wrong type", `{"username": 5, "password": "x", "client": "web"}`, "invalid_request", nil},
		{"missing fields", `{"password":"x"}`, "validation_failed", map[string]string{"username": "required", "client": "required"}},
		{"bad enum", `{"username":"a","password":"x","client":"desktop"}`, "validation_failed", map[string]string{"client": "oneof"}},
		{"too long", `{"username":"a","password":"` + strings.Repeat("x", 129) + `","client":"web"}`, "validation_failed", map[string]string{"password": "max"}},
	}
	for _, tt := range tests {
		_, err := bind(t, tt.body)
		coded, ok := errors.AsType[*Error](err)
		if !ok || coded.Code() != tt.code {
			t.Errorf("%s: error = %v, want %s", tt.name, err, tt.code)
			continue
		}
		if tt.fields == nil {
			continue
		}
		fields, _ := coded.Details()["fields"].(map[string]string)
		if len(fields) != len(tt.fields) {
			t.Errorf("%s: fields = %v, want %v", tt.name, fields, tt.fields)
		}
		for k, v := range tt.fields {
			if fields[k] != v {
				t.Errorf("%s: fields[%s] = %q, want %q (by JSON name)", tt.name, k, fields[k], v)
			}
		}
	}
}

func TestBindJSONKeepsTheSizeLimit(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"username":"`+strings.Repeat("a", 100)+`"}`))
	c.Request.Body = http.MaxBytesReader(w, c.Request.Body, 10)

	err := BindJSON(c, &loginBody{})
	Fail(c, slog.New(slog.DiscardHandler), err)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

func TestNullable(t *testing.T) {
	for body, want := range map[string]Nullable[string]{
		`{}`:               {},
		`{"locale": null}`: {Set: true, Null: true},
		`{"locale": "en"}`: {Set: true, Value: "en"},
	} {
		var dst struct {
			Locale Nullable[string] `json:"locale"`
		}
		if err := json.Unmarshal([]byte(body), &dst); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if dst.Locale != want {
			t.Errorf("%s: %+v, want %+v", body, dst.Locale, want)
		}
	}
	if p := (Nullable[string]{Set: true, Value: "en"}).Ptr(); p == nil || *p != "en" {
		t.Error("Ptr of a value isn't the value")
	}
	if (Nullable[string]{Set: true, Null: true}).Ptr() != nil || (Nullable[string]{}).Ptr() != nil {
		t.Error("Ptr of null or absent isn't nil")
	}
}
