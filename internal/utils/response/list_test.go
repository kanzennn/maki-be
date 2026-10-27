package response

import (
	"encoding/json"
	"testing"

	"github.com/kervzent/maki-be/internal/utils/pagination"
)

func TestNewListIsNeverNull(t *testing.T) {
	b, err := json.Marshal(NewList[string](nil, pagination.Page{Number: 1, Size: 20}, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"data":[],"meta":{"page":1,"page_size":20,"total":0}}` {
		t.Errorf("json = %s", b)
	}
}
