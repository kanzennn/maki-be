package dbtest

import (
	"fmt"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/utils/id"
)

func TestTestSchemaOnlyMatchesGeneratedNames(t *testing.T) {
	generated := fmt.Sprintf("test_%d_%s", time.Now().Unix(), id.New())
	if !testSchema.MatchString(generated) {
		t.Errorf("testSchema doesn't match a generated name %q", generated)
	}
	for _, name := range []string{
		"test_2024_backup",
		"test_1727000000_notacuid",
		"test_1727000000_tz4a98xxat96iws9zmbrgj3a_old",
		"public",
	} {
		if testSchema.MatchString(name) {
			t.Errorf("testSchema matches %q, which the sweep would drop", name)
		}
	}
}
