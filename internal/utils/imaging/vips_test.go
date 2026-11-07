//go:build vips

package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	Start()
	code := m.Run()
	Stop()
	os.Exit(code)
}

// withOrientation adds an EXIF block saying how the JPEG is turned, and a
// marker that must not survive the conversion.
func withOrientation(t *testing.T, data []byte, orientation uint16) []byte {
	t.Helper()
	var tiff bytes.Buffer
	tiff.WriteString("II*\x00")
	le := binary.LittleEndian
	_ = binary.Write(&tiff, le, uint32(8))      // first directory
	_ = binary.Write(&tiff, le, uint16(2))      // two entries
	_ = binary.Write(&tiff, le, uint16(0x0112)) // Orientation
	_ = binary.Write(&tiff, le, uint16(3))      // SHORT
	_ = binary.Write(&tiff, le, uint32(1))
	_ = binary.Write(&tiff, le, uint32(orientation))
	_ = binary.Write(&tiff, le, uint16(0x010f)) // Make
	_ = binary.Write(&tiff, le, uint16(2))      // ASCII
	_ = binary.Write(&tiff, le, uint32(10))
	_ = binary.Write(&tiff, le, uint32(38)) // after the directory
	_ = binary.Write(&tiff, le, uint32(0))  // no next directory
	tiff.WriteString("SECRETCAM\x00")
	app1 := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write(data[:2]) // SOI
	out.Write([]byte{0xff, 0xe1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(app1)+2)) //nolint:gosec // a short test block
	out.Write(app1)
	out.Write(data[2:])
	return out.Bytes()
}

// webpChunks lists the chunks of a WebP file, whose metadata would be its
// EXIF and XMP ones.
func webpChunks(t *testing.T, data []byte) []string {
	t.Helper()
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatalf("not a WebP: %q", data[:min(len(data), 12)])
	}
	var chunks []string
	for at := 12; at+8 <= len(data); {
		chunks = append(chunks, string(data[at:at+4]))
		size := int(binary.LittleEndian.Uint32(data[at+4:]))
		at += 8 + size + size%2
	}
	return chunks
}

func TestConvertMakesWebPOfEachSize(t *testing.T) {
	sizes := []Size{{800, 800}, {200, 200}}
	out, err := Converter{}.Convert(encodeJPEG(t, 1600, 1000), sizes)
	if err != nil {
		t.Fatal(err)
	}
	for i, size := range sizes {
		h, err := Inspect(out[i], 8000)
		if err != nil {
			t.Fatal(err)
		}
		if h.Format != "webp" || h.Width != size.Width || h.Height != size.Height {
			t.Errorf("size %d: %+v", i, h)
		}
	}
}

func TestConvertTakesEveryFormat(t *testing.T) {
	webp, err := Converter{}.Convert(encodePNG(t, 500, 500), []Size{{400, 400}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Converter{}).Convert(webp[0], []Size{{200, 200}}); err != nil {
		t.Errorf("a WebP upload: %v", err)
	}
}

func TestConvertNeverEnlarges(t *testing.T) {
	out, err := Converter{}.Convert(encodeJPEG(t, 100, 100), []Size{{800, 800}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := Inspect(out[0], 8000)
	if err != nil {
		t.Fatal(err)
	}
	if h.Width != 100 || h.Height != 100 {
		t.Errorf("got %d×%d", h.Width, h.Height)
	}
}

func TestConvertTurnsAndStrips(t *testing.T) {
	// Orientation 6: a phone held upright, the pixels on their side.
	data := withOrientation(t, encodeJPEG(t, 400, 200), 6)
	if !bytes.Contains(data, []byte("SECRETCAM")) {
		t.Fatal("the test image should carry the marker")
	}
	out, err := Converter{}.Convert(data, []Size{{1000, 1000}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := Inspect(out[0], 8000)
	if err != nil {
		t.Fatal(err)
	}
	if h.Width != 200 || h.Height != 400 {
		t.Errorf("not turned: %d×%d", h.Width, h.Height)
	}
	if bytes.Contains(out[0], []byte("SECRETCAM")) {
		t.Error("the camera's marker survived")
	}
	for _, chunk := range webpChunks(t, out[0]) {
		if chunk == "EXIF" || chunk == "XMP " {
			t.Errorf("metadata chunk %q survived, in %v", chunk, webpChunks(t, out[0]))
		}
	}
}

func TestConvertRejectsWhatItCannotRead(t *testing.T) {
	if _, err := (Converter{}).Convert([]byte("not an image"), []Size{{10, 10}}); !errors.Is(err, ErrRejected) {
		t.Errorf("garbage: %v", err)
	}
	// A good header on a broken body passes Inspect; libvips is the judge.
	broken := encodePNG(t, 100, 100)[:40]
	if _, err := Inspect(broken, 8000); err != nil {
		t.Fatalf("the header alone should pass: %v", err)
	}
	if _, err := (Converter{}).Convert(broken, []Size{{10, 10}}); !errors.Is(err, ErrRejected) {
		t.Errorf("broken body: %v", err)
	}
}
