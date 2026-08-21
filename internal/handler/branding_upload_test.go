package handler

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// pngOfSize encodes a real PNG of the given dimensions. A uniform image compresses
// extremely well, which is exactly the property a decompression bomb exploits: the bytes
// on the wire stay tiny while the decoded pixel buffer does not.
func pngOfSize(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	// Leave it uniformly zero - maximum compressibility.
	img.Set(0, 0, color.Gray{Y: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode %dx%d: %v", w, h, err)
	}
	return buf.Bytes()
}

// TestDecodeBrandingImage_rejectsDecompressionBomb is the real guard. The 5 MB body limit
// bounds bytes, not pixels, so without a dimension check a few hundred KB of PNG can
// allocate gigabytes. This builds an actual bomb rather than asserting against a stub.
func TestDecodeBrandingImage_rejectsDecompressionBomb(t *testing.T) {
	const dim = 12000 // 144 megapixels
	bomb := pngOfSize(t, dim, dim)

	// Establish the premise: this really is small enough to sail through the size limit.
	if len(bomb) > 5<<20 {
		t.Fatalf("test bomb is %d bytes, over the 5MB upload cap - it would be rejected by "+
			"size alone and would not exercise the dimension guard", len(bomb))
	}
	// Report megapixels rather than bytes: this fixture is grayscale, so it decodes to
	// image.Gray at 1 byte/px (~144 MB). The same dimensions in colour decode to NRGBA at
	// 4 bytes/px (~576 MB), which is the figure that matters for the cap.
	t.Logf("%dx%d PNG is only %d KB on the wire but %d megapixels decoded (~%d MB as Gray, ~%d MB as NRGBA)",
		dim, dim, len(bomb)/1024, (dim*dim)/1_000_000, (dim*dim)/(1<<20), (dim*dim*4)/(1<<20))

	img, userMsg, err := decodeBrandingImage(bytes.NewReader(bomb), "banner")
	if err != nil {
		t.Fatalf("unexpected internal error: %v", err)
	}
	if img != nil {
		t.Error("the bomb was decoded; the dimension guard did not fire")
	}
	if !strings.Contains(userMsg, "too large") {
		t.Errorf("userMsg = %q, want an explanation that the image is too large", userMsg)
	}
}

// A normal image must still work - a guard that rejects everything is not a fix.
func TestDecodeBrandingImage_acceptsOrdinaryImage(t *testing.T) {
	img, userMsg, err := decodeBrandingImage(bytes.NewReader(pngOfSize(t, 1200, 400)), "banner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userMsg != "" {
		t.Fatalf("ordinary 1200x400 image rejected: %s", userMsg)
	}
	if img == nil {
		t.Fatal("expected a decoded image")
	}
	if b := img.Bounds(); b.Dx() != 1200 || b.Dy() != 400 {
		t.Errorf("bounds = %v, want 1200x400", b)
	}
}

// A camera-sized photo is the case the cap must NOT break: someone uploading a phone or
// DSLR shot as a banner is ordinary use, not an attack.
func TestDecodeBrandingImage_acceptsCameraSizedPhoto(t *testing.T) {
	_, userMsg, err := decodeBrandingImage(bytes.NewReader(pngOfSize(t, 4032, 3024)), "banner") // 12 MP
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userMsg != "" {
		t.Errorf("a 12 MP photo should be accepted, got: %s", userMsg)
	}
}

// Non-images are refused on content type, before any decode is attempted.
func TestDecodeBrandingImage_rejectsNonImage(t *testing.T) {
	_, userMsg, err := decodeBrandingImage(strings.NewReader("<html><body>not an image</body></html>"), "logo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(userMsg, "must be JPEG") {
		t.Errorf("userMsg = %q, want a format rejection", userMsg)
	}
}

// TestDecodeBrandingImage_shortInputDoesNotMisclassify guards the io.ReadFull fix. A file
// under 512 bytes returns ErrUnexpectedEOF from ReadFull; treating that as a failure, or
// sniffing a short prefix, would reject a perfectly valid small image.
func TestDecodeBrandingImage_shortInputDoesNotMisclassify(t *testing.T) {
	tiny := pngOfSize(t, 1, 1)
	if len(tiny) >= 512 {
		t.Skipf("1x1 PNG is %d bytes, not short enough to exercise the path", len(tiny))
	}
	img, userMsg, err := decodeBrandingImage(bytes.NewReader(tiny), "logo")
	if err != nil {
		t.Fatalf("a %d-byte PNG produced an internal error: %v", len(tiny), err)
	}
	if userMsg != "" {
		t.Errorf("a valid %d-byte PNG was rejected: %s", len(tiny), userMsg)
	}
	if img == nil {
		t.Error("expected the small image to decode")
	}
}
