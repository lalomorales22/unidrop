package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManifestSigningRoundTripAndTamperRejection(t *testing.T) {
	root := t.TempDir()
	privatePath := filepath.Join(root, "release-private.pem")
	publicPath := filepath.Join(root, "release-public.pem")
	manifestPath := filepath.Join(root, "manifest.json")
	signaturePath := filepath.Join(root, "manifest.sig.json")
	if err := os.WriteFile(manifestPath, []byte("{\"version\":\"test\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := generateKey(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	if err := signManifest(privatePath, manifestPath, signaturePath); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(publicPath, manifestPath, signaturePath); err != nil {
		t.Fatalf("verify signed manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("{\"version\":\"tampered\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyManifest(publicPath, manifestPath, signaturePath); err == nil {
		t.Fatal("tampered manifest passed signature verification")
	}
}

func TestPrivateKeyPermissionsAreEnforced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode test")
	}
	root := t.TempDir()
	privatePath := filepath.Join(root, "release-private.pem")
	publicPath := filepath.Join(root, "release-public.pem")
	if err := generateKey(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(privatePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(privatePath); err == nil {
		t.Fatal("world-readable private key was accepted")
	}
}
