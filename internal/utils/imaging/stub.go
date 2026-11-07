//go:build !vips

package imaging

// Available reports whether this binary can convert images.
const Available = false

// Start prepares the converter: nothing to do without libvips.
func Start() {}

// Stop releases the converter: nothing to do without libvips.
func Stop() {}

// Converter converts uploads; without libvips it refuses.
type Converter struct{}

// Convert refuses: see the package's doc.
func (Converter) Convert([]byte, []Size) ([][]byte, error) {
	return nil, ErrUnavailable
}
