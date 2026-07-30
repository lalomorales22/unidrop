// UniDrop release signs and verifies release manifests with Ed25519 using only
// the Go standard library. Private keys are never accepted through arguments or
// environment variables; callers pass a path to a user-protected key file.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
)

type signatureEnvelope struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"keyId"`
	ManifestSHA256 string `json:"manifestSha256"`
	Signature      string `json:"signature"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "unidrop-release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected keygen, sign, or verify")
	}
	switch args[0] {
	case "keygen":
		flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
		privatePath := flags.String("private", "", "new private-key PEM path")
		publicPath := flags.String("public", "", "new public-key PEM path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *privatePath == "" || *publicPath == "" {
			return errors.New("keygen requires --private and --public")
		}
		return generateKey(*privatePath, *publicPath)
	case "sign":
		flags := flag.NewFlagSet("sign", flag.ContinueOnError)
		privatePath := flags.String("private", "", "private-key PEM path")
		manifestPath := flags.String("manifest", "", "release manifest path")
		signaturePath := flags.String("signature", "", "new signature-envelope path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *privatePath == "" || *manifestPath == "" || *signaturePath == "" {
			return errors.New("sign requires --private, --manifest, and --signature")
		}
		return signManifest(*privatePath, *manifestPath, *signaturePath)
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		publicPath := flags.String("public", "", "public-key PEM path")
		manifestPath := flags.String("manifest", "", "release manifest path")
		signaturePath := flags.String("signature", "", "signature-envelope path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *publicPath == "" || *manifestPath == "" || *signaturePath == "" {
			return errors.New("verify requires --public, --manifest, and --signature")
		}
		return verifyManifest(*publicPath, *manifestPath, *signaturePath)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func generateKey(privatePath, publicPath string) error {
	if _, err := os.Stat(privatePath); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("private key destination must not exist: %s", privatePath)
	}
	if _, err := os.Stat(publicPath); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("public key destination must not exist: %s", publicPath)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("encode private key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("encode public key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	if err := writeNewFile(privatePath, privatePEM, 0o600); err != nil {
		return err
	}
	if err := writeNewFile(publicPath, publicPEM, 0o644); err != nil {
		_ = os.Remove(privatePath)
		return err
	}
	return nil
}

func signManifest(privatePath, manifestPath, signaturePath string) error {
	privateKey, err := readPrivateKey(privatePath)
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	digest := sha256.Sum256(manifest)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	keyDigest := sha256.Sum256(publicKey)
	envelope := signatureEnvelope{
		SchemaVersion:  1,
		Algorithm:      "Ed25519",
		KeyID:          hex.EncodeToString(keyDigest[:]),
		ManifestSHA256: hex.EncodeToString(digest[:]),
		Signature:      base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifest)),
	}
	encoded, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return fmt.Errorf("encode signature: %w", err)
	}
	encoded = append(encoded, '\n')
	return writeNewFile(signaturePath, encoded, 0o644)
}

func verifyManifest(publicPath, manifestPath, signaturePath string) error {
	publicKey, err := readPublicKey(publicPath)
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	encoded, err := os.ReadFile(signaturePath)
	if err != nil {
		return fmt.Errorf("read signature: %w", err)
	}
	var envelope signatureEnvelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if envelope.SchemaVersion != 1 || envelope.Algorithm != "Ed25519" {
		return errors.New("unsupported signature envelope")
	}
	digest := sha256.Sum256(manifest)
	if envelope.ManifestSHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("manifest hash does not match signature envelope")
	}
	keyDigest := sha256.Sum256(publicKey)
	if envelope.KeyID != hex.EncodeToString(keyDigest[:]) {
		return errors.New("signature key ID does not match public key")
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		return errors.New("signature is not valid base64")
	}
	if !ed25519.Verify(publicKey, manifest, signature) {
		return errors.New("manifest signature is invalid")
	}
	return nil
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect private key: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private key permissions must deny group and other access")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	block, rest := pem.Decode(encoded)
	if block == nil || len(rest) != 0 || block.Type != "PRIVATE KEY" {
		return nil, errors.New("private key must be one PKCS#8 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private key is not valid PKCS#8")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not Ed25519")
	}
	return key, nil
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	block, rest := pem.Decode(encoded)
	if block == nil || len(rest) != 0 || block.Type != "PUBLIC KEY" {
		return nil, errors.New("public key must be one PKIX PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("public key is not valid PKIX")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key is not Ed25519")
	}
	return key, nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
