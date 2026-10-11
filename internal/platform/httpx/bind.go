package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// Errors for a request body that can't be used.
var (
	ErrInvalidRequest   = NewError(http.StatusBadRequest, "invalid_request")
	ErrValidationFailed = NewError(http.StatusUnprocessableEntity, "validation_failed")
)

// BindJSON decodes the request body into dst, a pointer to a struct, and
// checks its `binding` tags. A body that isn't JSON of that shape is 400
// invalid_request; fields that break a rule are 422 validation_failed, with
// details.fields mapping each one, by its JSON name, to the rule it broke:
// {"fields": {"username": "required"}}. A body over the size limit keeps
// its *http.MaxBytesError, which Fail turns into a 413.
func BindJSON(c *gin.Context, dst any) error {
	err := c.ShouldBindJSON(dst)
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return err
	}
	if verrs, ok := errors.AsType[validator.ValidationErrors](err); ok {
		fields := make(map[string]string, len(verrs))
		for _, fe := range verrs {
			fields[jsonName(dst, fe.StructField())] = fe.Tag()
		}
		return Invalid(fields)
	}
	return ErrInvalidRequest
}

// BindOptionalJSON is BindJSON for an endpoint whose body may be left out: an
// empty body leaves dst as it is. It checks no `binding` tags.
func BindOptionalJSON(c *gin.Context, dst any) error {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return err
		}
		return ErrInvalidRequest
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return ErrInvalidRequest
	}
	return nil
}

// Invalid returns validation_failed for fields, which maps JSON field names
// to the rule each broke — for checks a handler makes itself.
func Invalid(fields map[string]string) error {
	return ErrValidationFailed.WithDetails(map[string]any{"fields": fields})
}

// jsonName returns the JSON name of dst's field goField.
func jsonName(dst any, goField string) string {
	t := reflect.TypeOf(dst)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if f, ok := t.FieldByName(goField); ok {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			return name
		}
	}
	return goField
}

// Nullable is a JSON field that can be absent, null, or hold a value — the
// three things a PATCH body has to tell apart.
type Nullable[T any] struct {
	Set   bool // the field was in the body
	Null  bool // it was null
	Value T
}

// UnmarshalJSON records that the field was present. It isn't called for an
// absent field, which leaves Set false.
func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if string(b) == "null" {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// Ptr is the value, or nil when it is null or absent.
func (n Nullable[T]) Ptr() *T {
	if !n.Set || n.Null {
		return nil
	}
	return &n.Value
}
