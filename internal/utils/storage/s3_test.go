package storage

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/config"
)

// A stand-in for an S3 provider. It proves what the adapter sends and how it
// reads the answers; nothing here can prove a real provider's CORS or
// signature checks.
func fakeS3(t *testing.T, handler http.HandlerFunc) (public, private Storage) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	public, private = Open(config.Storage{
		Endpoint: srv.URL, Region: "auto", PublicBucket: "pub", PrivateBucket: "priv",
		AccessKey: "test-access", SecretKey: "test-secret", ForcePathStyle: true,
		PublicBaseURL: "https://cdn.example.com/",
	})
	return public, private
}

func TestOpenWithoutStorage(t *testing.T) {
	if public, private := Open(config.Storage{}); public != nil || private != nil {
		t.Fatal("no configuration should give no buckets")
	}
}

func TestPresignPutSignsTypeAndSize(t *testing.T) {
	_, private := fakeS3(t, func(http.ResponseWriter, *http.Request) { t.Error("presigning must not call the provider") })
	raw, err := private.PresignPut(t.Context(), "uploads/abc", "image/webp", 1234, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/priv/uploads/abc" {
		t.Errorf("path = %s", u.Path)
	}
	q := u.Query()
	signed := q.Get("X-Amz-SignedHeaders")
	for _, header := range []string{"content-length", "content-type", "host"} {
		if !strings.Contains(signed, header) {
			t.Errorf("signed headers %q lack %s", signed, header)
		}
	}
	if q.Get("X-Amz-Expires") != "300" {
		t.Errorf("expires = %s", q.Get("X-Amz-Expires"))
	}
	for name := range q {
		if strings.Contains(strings.ToLower(name), "checksum") {
			t.Errorf("a checksum in the address would be refused by some providers: %s", name)
		}
	}
}

func TestPublicURLUsesTheBase(t *testing.T) {
	public, _ := fakeS3(t, func(http.ResponseWriter, *http.Request) {})
	if got := public.URL("outlets/o/menus/m/a.webp"); got != "https://cdn.example.com/outlets/o/menus/m/a.webp" {
		t.Errorf("URL = %s", got)
	}
}

func TestNotFoundAnswers(t *testing.T) {
	_, private := fakeS3(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if _, err := private.Head(t.Context(), "uploads/none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Head: %v", err)
	}
	if _, err := private.Get(t.Context(), "uploads/none", 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get: %v", err)
	}
	if err := private.Delete(t.Context(), "uploads/none"); err != nil {
		t.Errorf("Delete of nothing: %v", err)
	}
}

func TestGetStopsAtTheLimit(t *testing.T) {
	_, private := fakeS3(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 11)) })
	if _, err := private.Get(t.Context(), "uploads/big", 10); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over the limit: %v", err)
	}
	if data, err := private.Get(t.Context(), "uploads/big", 11); err != nil || len(data) != 11 {
		t.Errorf("at the limit: %d bytes, %v", len(data), err)
	}
}

func TestPutSendsHeadersWithoutChecksum(t *testing.T) {
	public, _ := fakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut || r.URL.Path != "/pub/a.webp" || string(body) != "webp" {
			t.Errorf("%s %s %q", r.Method, r.URL.Path, body)
		}
		if r.Header.Get("Content-Type") != "image/webp" || r.Header.Get("Cache-Control") != "immutable" {
			t.Errorf("headers: %v", r.Header)
		}
		for name := range r.Header {
			if strings.Contains(strings.ToLower(name), "checksum") {
				t.Errorf("checksum header sent: %s", name)
			}
		}
	})
	if err := public.Put(t.Context(), "a.webp", "image/webp", "immutable", []byte("webp")); err != nil {
		t.Fatal(err)
	}
}

func TestListOlderFollowsPages(t *testing.T) {
	const page = `<ListBucketResult><IsTruncated>%t</IsTruncated><NextContinuationToken>next</NextContinuationToken>
		<Contents><Key>%s</Key><LastModified>%s</LastModified></Contents></ListBucketResult>`
	old, fresh := time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)
	_, private := fakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("prefix") != "uploads/" {
			t.Errorf("prefix = %s", r.URL.Query().Get("prefix"))
		}
		if r.URL.Query().Get("continuation-token") == "" {
			_, _ = fmt.Fprintf(w, page, true, "uploads/old", old)
			return
		}
		_, _ = fmt.Fprintf(w, page, false, "uploads/fresh", fresh)
	})
	keys, err := private.ListOlder(t.Context(), "uploads/", time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "uploads/old" {
		t.Errorf("keys = %v", keys)
	}
}
