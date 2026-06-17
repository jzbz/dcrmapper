package server

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/fstest"
)

func TestAssetVersion(t *testing.T) {
	data := []byte("body{color:red}")
	fsys := fstest.MapFS{
		"css/app.css": &fstest.MapFile{Data: data},
	}

	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])[:10]

	if got := assetVersion(fsys, "css/app.css"); got != want {
		t.Errorf("assetVersion(present) = %q, want %q", got, want)
	}
	if len(want) != 10 {
		t.Errorf("hash length = %d, want 10", len(want))
	}

	// Missing file and a nil FS both fall back to defaultAssetVersion.
	if got := assetVersion(fsys, "css/missing.css"); got != defaultAssetVersion {
		t.Errorf("assetVersion(missing) = %q, want \"dev\"", got)
	}
	if got := assetVersion(nil, "anything"); got != defaultAssetVersion {
		t.Errorf("assetVersion(nil fs) = %q, want \"dev\"", got)
	}
}
