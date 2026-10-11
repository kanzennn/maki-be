package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func bindPage(t *testing.T, query string) (Page, error) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items?"+query, nil)
	return BindPage(c)
}

func TestBindPage(t *testing.T) {
	for query, want := range map[string]Page{
		"":                        {Number: 1, Size: DefaultPageSize},
		"page=3":                  {Number: 3, Size: DefaultPageSize},
		"page=2&page_size=100":    {Number: 2, Size: 100},
		"page_size=1&other=thing": {Number: 1, Size: 1},
	} {
		got, err := bindPage(t, query)
		if err != nil || got != want {
			t.Errorf("?%s = %+v, %v; want %+v", query, got, err, want)
		}
	}
	if (Page{Number: 3, Size: 20}).Offset() != 40 {
		t.Error("the third page of 20 starts after 40 items")
	}
}

func TestBindPageRejects(t *testing.T) {
	for query, field := range map[string]string{
		"page=0":           "page",
		"page=-1":          "page",
		"page=two":         "page",
		"page_size=0":      "page_size",
		"page_size=101":    "page_size",
		"page_size=1.5":    "page_size",
		"page=1&page_size": "",
	} {
		_, err := bindPage(t, query)
		if field == "" {
			if err != nil {
				t.Errorf("?%s: %v, want the empty value ignored", query, err)
			}
			continue
		}
		var e *Error
		if !errors.As(err, &e) || e.Code() != "validation_failed" {
			t.Errorf("?%s: %v, want validation_failed", query, err)
			continue
		}
		fields, _ := e.Details()["fields"].(map[string]string)
		if _, ok := fields[field]; !ok {
			t.Errorf("?%s: fields %v, want %s named", query, e.Details()["fields"], field)
		}
	}
}

func TestNewListIsNeverNull(t *testing.T) {
	b, err := json.Marshal(NewList[string](nil, Page{Number: 1, Size: 20}, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"data":[],"meta":{"page":1,"page_size":20,"total":0}}` {
		t.Errorf("json = %s", b)
	}
}
