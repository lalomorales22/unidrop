// Xendfile update verifies and stages signed release artifacts and provides the
// fail-closed preference and replacement primitives used by native installers.
// It intentionally uses only the Go standard library.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	appversion "xendfile/internal/version"
)

const (
	maxManifestBytes   = int64(4 << 20)
	maxSignatureBytes  = int64(128 << 10)
	maxUpdateBytes     = int64(2 << 30)
	maxLocalStateBytes = int64(64 << 10)
	metadataClockSkew  = 5 * time.Minute
	maximumValidity    = 45 * 24 * time.Hour
)

const (
	preferenceNotifyOnly = "notify-only"
	preferenceAutomatic  = "automatic"
	preferenceDisabled   = "disabled"
)

const (
	replacementPrepared    = "prepared"
	replacementTargetMoved = "target-moved"
	replacementReplaced    = "replaced"
	replacementHealthy     = "healthy"
)

type signatureEnvelope struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Algorithm      string `json:"algorithm"`
	KeyID          string `json:"keyId"`
	ManifestSHA256 string `json:"manifestSha256"`
	Signature      string `json:"signature"`
}

type releaseManifest struct {
	SchemaVersion            int               `json:"schemaVersion"`
	Product                  string            `json:"product"`
	Version                  string            `json:"version"`
	Tag                      string            `json:"tag"`
	SourceCommit             string            `json:"sourceCommit"`
	PublishedAt              string            `json:"publishedAt"`
	ExpiresAt                string            `json:"expiresAt"`
	ProtocolVersion          int               `json:"protocolVersion"`
	MinimumCompatibleVersion string            `json:"minimumCompatibleVersion"`
	RevokedVersions          []string          `json:"revokedVersions"`
	Artifacts                []releaseArtifact `json:"artifacts"`
}

type releaseArtifact struct {
	Name                     string `json:"name"`
	URL                      string `json:"url"`
	Component                string `json:"component"`
	Platform                 string `json:"platform"`
	Architecture             string `json:"architecture"`
	ProtocolVersion          int    `json:"protocolVersion"`
	MinimumCompatibleVersion string `json:"minimumCompatibleVersion"`
	Size                     int64  `json:"size"`
	SHA256                   string `json:"sha256"`
}

type updateOptions struct {
	CurrentVersion  string
	HighestVersion  string
	Platform        string
	Architecture    string
	Component       string
	ApprovedHosts   map[string]struct{}
	Now             time.Time
	MaximumArtifact int64
}

type updateState struct {
	HighestAcceptedVersion string `json:"highestAcceptedVersion"`
	AcceptedAt             string `json:"acceptedAt"`
}

type updatePreferenceFile struct {
	SchemaVersion int    `json:"schemaVersion"`
	Mode          string `json:"mode"`
	UpdatedAt     string `json:"updatedAt"`
}

type replacementJournal struct {
	SchemaVersion     int    `json:"schemaVersion"`
	Phase             string `json:"phase"`
	TargetPath        string `json:"targetPath"`
	CandidatePath     string `json:"candidatePath"`
	PendingBackupPath string `json:"pendingBackupPath"`
	BackupPath        string `json:"backupPath"`
	PreviousSize      int64  `json:"previousSize"`
	PreviousSHA256    string `json:"previousSha256"`
	CandidateSize     int64  `json:"candidateSize"`
	CandidateSHA256   string `json:"candidateSha256"`
	UpdatedAt         string `json:"updatedAt"`
}

type stageResult struct {
	Version  string
	Artifact releaseArtifact
	Path     string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "xendfile-update:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected: stage, preference, or recover")
	}
	switch args[0] {
	case "stage":
		return runStage(args[1:])
	case "preference":
		return runPreference(args[1:])
	case "recover":
		return runRecover(args[1:])
	default:
		return errors.New("expected: stage, preference, or recover")
	}
}

func runStage(args []string) error {
	flags := flag.NewFlagSet("stage", flag.ContinueOnError)
	manifestURL := flags.String("manifest-url", "", "HTTPS release manifest URL")
	signatureURL := flags.String("signature-url", "", "HTTPS signature-envelope URL")
	publicPath := flags.String("public-key", "", "trusted Ed25519 public-key PEM")
	component := flags.String("component", "core", "release component to stage")
	stagingDir := flags.String("staging-dir", "", "private update staging directory")
	statePath := flags.String("state", "", "highest-accepted-version state file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *manifestURL == "" || *publicPath == "" {
		return errors.New("stage requires --manifest-url and --public-key")
	}
	if *signatureURL == "" {
		if !strings.HasSuffix(*manifestURL, ".json") {
			return errors.New("--signature-url is required when the manifest URL does not end in .json")
		}
		*signatureURL = strings.TrimSuffix(*manifestURL, ".json") + ".sig.json"
	}
	manifestParsed, err := checkedHTTPSURL(*manifestURL)
	if err != nil {
		return fmt.Errorf("manifest URL: %w", err)
	}
	if _, err := checkedHTTPSURL(*signatureURL); err != nil {
		return fmt.Errorf("signature URL: %w", err)
	}
	publicKey, err := readPublicKey(*publicPath)
	if err != nil {
		return err
	}
	if *stagingDir == "" || *statePath == "" {
		cache, cacheErr := os.UserCacheDir()
		config, configErr := os.UserConfigDir()
		if cacheErr != nil || configErr != nil {
			return errors.New("resolve per-user update directories")
		}
		if *stagingDir == "" {
			*stagingDir = filepath.Join(cache, "Xendfile", "updates")
		}
		if *statePath == "" {
			*statePath = filepath.Join(config, "Xendfile", "update-state.json")
		}
	}
	highest, err := readHighestAccepted(*statePath)
	if err != nil {
		return err
	}
	keyID := sha256.Sum256(publicKey)
	approvedHosts := map[string]struct{}{canonicalHost(manifestParsed): {}}
	if strings.EqualFold(manifestParsed.Hostname(), "github.com") {
		approvedHosts["release-assets.githubusercontent.com"] = struct{}{}
	}
	options := updateOptions{
		CurrentVersion:  appversion.Current,
		HighestVersion:  highest,
		Platform:        runtime.GOOS,
		Architecture:    runtime.GOARCH,
		Component:       *component,
		ApprovedHosts:   approvedHosts,
		Now:             time.Now().UTC(),
		MaximumArtifact: maxUpdateBytes,
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	result, err := stageUpdate(context.Background(), client, *manifestURL, *signatureURL,
		map[string]ed25519.PublicKey{hex.EncodeToString(keyID[:]): publicKey}, options, *stagingDir, *statePath)
	if err != nil {
		return err
	}
	fmt.Printf("Xendfile %s verified and staged at %s\n", result.Version, result.Path)
	return nil
}

func runPreference(args []string) error {
	if len(args) == 0 || (args[0] != "get" && args[0] != "set") {
		return errors.New("expected: preference get [--file FILE] or preference set MODE [--file FILE]")
	}
	command := args[0]
	mode := ""
	remaining := args[1:]
	if command == "set" {
		if len(remaining) == 0 {
			return errors.New("preference set requires automatic, notify-only, or disabled")
		}
		mode = remaining[0]
		remaining = remaining[1:]
	}
	flags := flag.NewFlagSet("preference "+command, flag.ContinueOnError)
	preferencePath := flags.String("file", "", "private update-preference file")
	if err := flags.Parse(remaining); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected preference arguments")
	}
	if *preferencePath == "" {
		resolved, err := defaultPreferencePath()
		if err != nil {
			return err
		}
		*preferencePath = resolved
	}
	if command == "set" {
		if err := writeUpdatePreference(*preferencePath, mode, time.Now().UTC()); err != nil {
			return err
		}
	}
	selected, err := readUpdatePreference(*preferencePath)
	if err != nil {
		return err
	}
	fmt.Println(selected)
	return nil
}

func runRecover(args []string) error {
	flags := flag.NewFlagSet("recover", flag.ContinueOnError)
	journalPath := flags.String("journal", "", "private replacement journal")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *journalPath == "" || flags.NArg() != 0 {
		return errors.New("recover requires --journal FILE")
	}
	absoluteJournal, err := filepath.Abs(*journalPath)
	if err != nil {
		return err
	}
	return recoverReplacement(absoluteJournal)
}

func stageUpdate(ctx context.Context, baseClient *http.Client, manifestURL, signatureURL string,
	trustedKeys map[string]ed25519.PublicKey, options updateOptions, stagingDir, statePath string) (*stageResult, error) {
	if len(trustedKeys) == 0 {
		return nil, errors.New("no trusted update keys configured")
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	if options.MaximumArtifact <= 0 {
		options.MaximumArtifact = maxUpdateBytes
	}
	if len(options.ApprovedHosts) == 0 {
		return nil, errors.New("no approved release hosts configured")
	}
	for label, rawURL := range map[string]string{"manifest": manifestURL, "signature": signatureURL} {
		parsed, err := checkedHTTPSURL(rawURL)
		if err != nil {
			return nil, fmt.Errorf("%s URL: %w", label, err)
		}
		if _, approved := options.ApprovedHosts[canonicalHost(parsed)]; !approved {
			return nil, fmt.Errorf("%s host is not approved", label)
		}
	}
	client := secureHTTPClient(baseClient, options.ApprovedHosts)
	manifestBytes, err := fetchLimited(ctx, client, manifestURL, maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	signatureBytes, err := fetchLimited(ctx, client, signatureURL, maxSignatureBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch signature: %w", err)
	}
	manifest, artifact, err := verifyUpdate(manifestBytes, signatureBytes, trustedKeys, options)
	if err != nil {
		return nil, err
	}
	stagedPath, err := downloadArtifact(ctx, client, artifact, stagingDir, options.MaximumArtifact)
	if err != nil {
		return nil, err
	}
	if err := writeHighestAccepted(statePath, manifest.Version, options.Now); err != nil {
		_ = os.Remove(stagedPath)
		return nil, err
	}
	return &stageResult{Version: manifest.Version, Artifact: *artifact, Path: stagedPath}, nil
}

func verifyUpdate(manifestBytes, signatureBytes []byte, trustedKeys map[string]ed25519.PublicKey, options updateOptions) (*releaseManifest, *releaseArtifact, error) {
	var envelope signatureEnvelope
	if err := decodeStrictJSON(signatureBytes, &envelope); err != nil {
		return nil, nil, fmt.Errorf("invalid signature envelope: %w", err)
	}
	if envelope.SchemaVersion != 1 || envelope.Algorithm != "Ed25519" {
		return nil, nil, errors.New("unsupported signature envelope")
	}
	publicKey, ok := trustedKeys[envelope.KeyID]
	if !ok {
		return nil, nil, errors.New("manifest is signed by an untrusted key")
	}
	keyDigest := sha256.Sum256(publicKey)
	if envelope.KeyID != hex.EncodeToString(keyDigest[:]) {
		return nil, nil, errors.New("trusted key ID is inconsistent")
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	if envelope.ManifestSHA256 != hex.EncodeToString(manifestDigest[:]) {
		return nil, nil, errors.New("manifest hash does not match signature envelope")
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, nil, errors.New("manifest signature is malformed")
	}
	if !ed25519.Verify(publicKey, manifestBytes, signature) {
		return nil, nil, errors.New("manifest signature is invalid")
	}

	var manifest releaseManifest
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		return nil, nil, fmt.Errorf("invalid signed manifest: %w", err)
	}
	if err := validateManifest(&manifest, options); err != nil {
		return nil, nil, err
	}
	var selected *releaseArtifact
	previousName := ""
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	for index := range manifest.Artifacts {
		artifact := &manifest.Artifacts[index]
		if err := validateArtifact(artifact, &manifest, options); err != nil {
			return nil, nil, fmt.Errorf("invalid artifact %q: %w", artifact.Name, err)
		}
		if _, exists := seen[artifact.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate artifact %q", artifact.Name)
		}
		seen[artifact.Name] = struct{}{}
		if previousName != "" && artifact.Name < previousName {
			return nil, nil, errors.New("artifact list is not sorted")
		}
		previousName = artifact.Name
		if artifact.Component == options.Component && artifact.Platform == options.Platform && artifact.Architecture == options.Architecture {
			if selected != nil {
				return nil, nil, errors.New("manifest contains multiple matching artifacts")
			}
			selected = artifact
		}
	}
	if selected == nil {
		return nil, nil, fmt.Errorf("no %s update for %s/%s", options.Component, options.Platform, options.Architecture)
	}
	return &manifest, selected, nil
}

func validateManifest(manifest *releaseManifest, options updateOptions) error {
	if manifest.SchemaVersion != 1 || manifest.Product != "Xendfile" {
		return errors.New("unsupported release manifest")
	}
	if _, err := parseVersion(manifest.Version); err != nil {
		return fmt.Errorf("invalid release version: %w", err)
	}
	if manifest.Tag != "v"+manifest.Version {
		return errors.New("release tag does not match version")
	}
	if !isLowerHex(manifest.SourceCommit, 40) {
		return errors.New("source commit is malformed")
	}
	published, err := time.Parse(time.RFC3339, manifest.PublishedAt)
	if err != nil {
		return errors.New("publishedAt is malformed")
	}
	expires, err := time.Parse(time.RFC3339, manifest.ExpiresAt)
	if err != nil {
		return errors.New("expiresAt is malformed")
	}
	if published.After(options.Now.Add(metadataClockSkew)) {
		return errors.New("release metadata is from the future")
	}
	if !expires.After(options.Now) {
		return errors.New("release metadata has expired")
	}
	if !expires.After(published) || expires.Sub(published) > maximumValidity {
		return errors.New("release metadata validity window is invalid")
	}
	if manifest.ProtocolVersion != appversion.Protocol {
		return errors.New("release protocol version does not match this client")
	}
	if compareVersion(manifest.MinimumCompatibleVersion, appversion.MinimumCompatibleVersion) < 0 {
		return errors.New("release lowers the compatibility floor")
	}
	if compareVersion(manifest.MinimumCompatibleVersion, manifest.Version) > 0 {
		return errors.New("minimum compatible version exceeds release version")
	}
	if compareVersion(manifest.Version, options.CurrentVersion) <= 0 {
		return errors.New("release is not newer than the installed version")
	}
	if options.HighestVersion != "" && compareVersion(manifest.Version, options.HighestVersion) <= 0 {
		return errors.New("release would roll back the highest accepted version")
	}
	seenRevoked := map[string]struct{}{}
	for _, revoked := range manifest.RevokedVersions {
		if _, err := parseVersion(revoked); err != nil {
			return errors.New("revoked version is malformed")
		}
		if _, exists := seenRevoked[revoked]; exists {
			return errors.New("revoked version list contains duplicates")
		}
		seenRevoked[revoked] = struct{}{}
		if revoked == manifest.Version {
			return errors.New("release version is revoked")
		}
	}
	if len(manifest.Artifacts) == 0 {
		return errors.New("release manifest has no artifacts")
	}
	return nil
}

func validateArtifact(artifact *releaseArtifact, manifest *releaseManifest, options updateOptions) error {
	if artifact.Name == "" || strings.ContainsAny(artifact.Name, `/\\`) || artifact.Name == "." || artifact.Name == ".." {
		return errors.New("unsafe artifact name")
	}
	parsed, err := checkedHTTPSURL(artifact.URL)
	if err != nil {
		return err
	}
	if _, allowed := options.ApprovedHosts[canonicalHost(parsed)]; !allowed {
		return errors.New("artifact host is not approved")
	}
	decodedName, err := url.PathUnescape(path.Base(parsed.EscapedPath()))
	if err != nil || decodedName != artifact.Name {
		return errors.New("artifact URL does not bind its filename")
	}
	if artifact.Component == "" || artifact.Platform == "" || artifact.Architecture == "" {
		return errors.New("artifact target is incomplete")
	}
	if artifact.ProtocolVersion != manifest.ProtocolVersion || artifact.MinimumCompatibleVersion != manifest.MinimumCompatibleVersion {
		return errors.New("artifact compatibility metadata does not match manifest")
	}
	if artifact.Size <= 0 || artifact.Size > options.MaximumArtifact {
		return errors.New("artifact size is outside the accepted limit")
	}
	if !isLowerHex(artifact.SHA256, sha256.Size*2) {
		return errors.New("artifact SHA-256 is malformed")
	}
	return nil
}

func fetchLimited(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	if _, err := checkedHTTPSURL(rawURL); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Xendfile-Update/"+appversion.Current)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return nil, errors.New("response exceeds size limit")
	}
	encoded, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > limit {
		return nil, errors.New("response exceeds size limit")
	}
	return encoded, nil
}

func downloadArtifact(ctx context.Context, client *http.Client, artifact *releaseArtifact, stagingDir string, maximum int64) (result string, resultErr error) {
	if artifact.Size > maximum {
		return "", errors.New("artifact exceeds configured size limit")
	}
	if err := ensurePrivateDirectory(stagingDir); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "Xendfile-Update/"+appversion.Current)
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download update: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download update: HTTP status %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != artifact.Size {
		return "", errors.New("download size does not match signed metadata")
	}
	file, err := os.CreateTemp(stagingDir, ".xendfile-update-*")
	if err != nil {
		return "", fmt.Errorf("create non-executable staging file: %w", err)
	}
	stagedPath := file.Name()
	defer func() {
		if resultErr != nil {
			_ = file.Close()
			_ = os.Remove(stagedPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", err
	}
	hasher := sha256.New()
	written, err := copyAndHash(file, response.Body, hasher, artifact.Size)
	if err != nil {
		return "", fmt.Errorf("download update: %w", err)
	}
	if written != artifact.Size {
		return "", errors.New("download ended before the signed byte count")
	}
	if hex.EncodeToString(hasher.Sum(nil)) != artifact.SHA256 {
		return "", errors.New("download SHA-256 does not match signed metadata")
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return stagedPath, nil
}

func copyAndHash(destination io.Writer, source io.Reader, hasher hash.Hash, expected int64) (int64, error) {
	written, err := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(source, expected+1))
	if err != nil {
		return written, err
	}
	if written > expected {
		return written, errors.New("download exceeds the signed byte count")
	}
	return written, nil
}

func secureHTTPClient(base *http.Client, approved map[string]struct{}) *http.Client {
	clone := *base
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		parsed, err := checkedHTTPSURL(request.URL.String())
		if err != nil {
			return err
		}
		if _, ok := approved[canonicalHost(parsed)]; !ok {
			return errors.New("redirect leaves approved release hosts")
		}
		return nil
	}
	return &clone
}

func checkedHTTPSURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("URL must be absolute HTTPS")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("URL must not contain credentials or a fragment")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("URL host is empty")
	}
	return parsed, nil
}

func canonicalHost(parsed *url.URL) string {
	return strings.ToLower(parsed.Host)
}

func decodeStrictJSON(encoded []byte, target any) error {
	if len(encoded) == 0 {
		return errors.New("empty JSON")
	}
	validator := json.NewDecoder(strings.NewReader(string(encoded)))
	if err := validateJSONValue(validator); err != nil {
		return err
	}
	if _, err := validator.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains trailing data")
		}
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON field %q", key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func ensurePrivateDirectory(directory string) error {
	before, beforeErr := os.Lstat(directory)
	if beforeErr != nil && !errors.Is(beforeErr, os.ErrNotExist) {
		return beforeErr
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("staging path must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("staging directory permissions are not private")
	}
	if beforeErr == nil && !os.SameFile(before, info) {
		return errors.New("staging directory changed while it was checked")
	}
	return nil
}

func readHighestAccepted(statePath string) (string, error) {
	encoded, exists, err := readPrivateFileBytes(statePath)
	if !exists && err == nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read update state: %w", err)
	}
	var state updateState
	if err := decodeStrictJSON(encoded, &state); err != nil {
		return "", fmt.Errorf("invalid update state: %w", err)
	}
	if _, err := parseVersion(state.HighestAcceptedVersion); err != nil {
		return "", errors.New("update state contains an invalid version")
	}
	if _, err := time.Parse(time.RFC3339, state.AcceptedAt); err != nil {
		return "", errors.New("update state contains an invalid acceptance time")
	}
	return state.HighestAcceptedVersion, nil
}

func writeHighestAccepted(statePath, version string, acceptedAt time.Time) error {
	if err := ensurePrivateDirectory(filepath.Dir(statePath)); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(updateState{
		HighestAcceptedVersion: version,
		AcceptedAt:             acceptedAt.UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if info, err := os.Lstat(statePath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("update state must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if runtime.GOOS == "windows" {
		return writeStateDirect(statePath, encoded)
	}
	file, err := os.CreateTemp(filepath.Dir(statePath), ".update-state-*")
	if err != nil {
		return fmt.Errorf("create update state: %w", err)
	}
	temporary := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, statePath); err != nil {
		return fmt.Errorf("replace update state: %w", err)
	}
	ok = true
	return nil
}

func writeStateDirect(statePath string, encoded []byte) error {
	file, err := os.OpenFile(statePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open update state: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func defaultPreferencePath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("resolve per-user update preference directory")
	}
	return filepath.Join(config, "Xendfile", "update-preference.json"), nil
}

func validUpdatePreference(mode string) bool {
	return mode == preferenceNotifyOnly || mode == preferenceAutomatic || mode == preferenceDisabled
}

func readUpdatePreference(preferencePath string) (string, error) {
	var preference updatePreferenceFile
	exists, err := readPrivateJSONFile(preferencePath, &preference)
	if err != nil {
		return "", fmt.Errorf("read update preference: %w", err)
	}
	if !exists {
		return preferenceNotifyOnly, nil
	}
	if preference.SchemaVersion != 1 || !validUpdatePreference(preference.Mode) {
		return "", errors.New("update preference is invalid")
	}
	if _, err := time.Parse(time.RFC3339, preference.UpdatedAt); err != nil {
		return "", errors.New("update preference timestamp is invalid")
	}
	return preference.Mode, nil
}

func writeUpdatePreference(preferencePath, mode string, updatedAt time.Time) error {
	if !validUpdatePreference(mode) {
		return errors.New("update preference must be automatic, notify-only, or disabled")
	}
	if updatedAt.IsZero() {
		return errors.New("update preference timestamp is required")
	}
	return writeAtomicPrivateJSON(preferencePath, updatePreferenceFile{
		SchemaVersion: 1,
		Mode:          mode,
		UpdatedAt:     updatedAt.UTC().Format(time.RFC3339),
	})
}

func readPrivateJSONFile(filePath string, target any) (bool, error) {
	if err := recoverAtomicPrivateFile(filePath); err != nil {
		return false, err
	}
	encoded, exists, err := readPrivateFileBytes(filePath)
	if err != nil || !exists {
		return exists, err
	}
	if err := decodeStrictJSON(encoded, target); err != nil {
		return false, err
	}
	return true, nil
}

func readPrivateFileBytes(filePath string) ([]byte, bool, error) {
	before, err := privateRegularFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if before.Size() > maxLocalStateBytes {
		return nil, false, errors.New("private state exceeds size limit")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, false, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, false, errors.New("private state changed while it was opened")
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, maxLocalStateBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, false, readErr
	}
	if closeErr != nil {
		return nil, false, closeErr
	}
	if int64(len(encoded)) > maxLocalStateBytes {
		return nil, false, errors.New("private state exceeds size limit")
	}
	after, err := privateRegularFile(filePath)
	if err != nil || !os.SameFile(before, after) || after.Size() != int64(len(encoded)) {
		return nil, false, errors.New("private state changed while it was read")
	}
	return encoded, true, nil
}

func writeAtomicPrivateJSON(filePath string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if int64(len(encoded)) > maxLocalStateBytes {
		return errors.New("private state exceeds size limit")
	}
	directory := filepath.Dir(filePath)
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	if err := recoverAtomicPrivateFile(filePath); err != nil {
		return err
	}
	_, existingErr := privateRegularFile(filePath)
	if existingErr != nil && !errors.Is(existingErr, os.ErrNotExist) {
		return existingErr
	}
	file, err := os.CreateTemp(directory, ".private-state-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	previous := filePath + ".previous"
	if existingErr == nil {
		if err := os.Rename(filePath, previous); err != nil {
			return fmt.Errorf("preserve previous private state: %w", err)
		}
		if err := syncDirectory(directory); err != nil {
			_ = os.Rename(previous, filePath)
			return err
		}
	}
	if err := os.Rename(temporary, filePath); err != nil {
		if existingErr == nil {
			_ = os.Rename(previous, filePath)
		}
		return fmt.Errorf("replace private state: %w", err)
	}
	keepTemporary = false
	if err := syncDirectory(directory); err != nil {
		return err
	}
	if existingErr == nil {
		if err := removePrivateRegularFile(previous); err != nil {
			return err
		}
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func recoverAtomicPrivateFile(filePath string) error {
	previous := filePath + ".previous"
	_, currentErr := privateRegularFile(filePath)
	_, previousErr := privateRegularFile(previous)
	currentExists := currentErr == nil
	previousExists := previousErr == nil
	if currentErr != nil && !errors.Is(currentErr, os.ErrNotExist) {
		return currentErr
	}
	if previousErr != nil && !errors.Is(previousErr, os.ErrNotExist) {
		return previousErr
	}
	if !currentExists && previousExists {
		if err := os.Rename(previous, filePath); err != nil {
			return fmt.Errorf("recover previous private state: %w", err)
		}
		return syncDirectory(filepath.Dir(filePath))
	}
	if currentExists && previousExists {
		if err := removePrivateRegularFile(previous); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(filePath))
	}
	return nil
}

func privateRegularFile(filePath string) (os.FileInfo, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("private state must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private state permissions are not private")
	}
	return info, nil
}

func regularFile(filePath string) (os.FileInfo, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("replacement path must be a regular file")
	}
	return info, nil
}

func removePrivateRegularFile(filePath string) error {
	if _, err := privateRegularFile(filePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Remove(filePath)
}

func removeRegularFile(filePath string) error {
	if _, err := regularFile(filePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Remove(filePath)
}

func clearPrivateState(filePath string) error {
	if err := removePrivateRegularFile(filePath + ".previous"); err != nil {
		return err
	}
	if err := removePrivateRegularFile(filePath); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(filePath))
}

func syncDirectory(directoryPath string) error {
	directory, err := os.Open(directoryPath)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if runtime.GOOS == "windows" {
		return closeErr
	}
	if err != nil {
		return err
	}
	return closeErr
}

func replaceVerifiedArtifact(stagedPath, targetPath, journalPath string, expectedSize int64, expectedSHA256 string, healthCheck func(string) error) (string, error) {
	if healthCheck == nil {
		return "", errors.New("replacement health check is required")
	}
	if expectedSize <= 0 || !isLowerHex(expectedSHA256, sha256.Size*2) {
		return "", errors.New("replacement requires a signed size and SHA-256")
	}
	absoluteStaged, err := filepath.Abs(stagedPath)
	if err != nil {
		return "", err
	}
	absoluteTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return "", err
	}
	absoluteJournal, err := filepath.Abs(journalPath)
	if err != nil {
		return "", err
	}
	if !distinctPaths(absoluteStaged, absoluteTarget, absoluteJournal, absoluteJournal+".previous") {
		return "", errors.New("staged, installed, and journal paths must differ")
	}
	if err := existingPathsAreDistinct(absoluteStaged, absoluteTarget, absoluteJournal, absoluteJournal+".previous"); err != nil {
		return "", err
	}
	if err := recoverReplacement(absoluteJournal); err != nil {
		return "", fmt.Errorf("recover prior replacement: %w", err)
	}
	targetInfo, err := regularFile(absoluteTarget)
	if err != nil {
		return "", fmt.Errorf("inspect working installation: %w", err)
	}
	previousSize, previousDigest, err := fileSizeAndSHA256(absoluteTarget)
	if err != nil {
		return "", err
	}
	stagedSize, stagedDigest, err := fileSizeAndSHA256(absoluteStaged)
	if err != nil {
		return "", fmt.Errorf("inspect staged update: %w", err)
	}
	if stagedSize != expectedSize || stagedDigest != expectedSHA256 {
		return "", errors.New("staged update no longer matches signed metadata")
	}
	targetDirectory := filepath.Dir(absoluteTarget)
	pendingBackup := absoluteTarget + ".previous.pending"
	backupPath := absoluteTarget + ".last-working"
	if !distinctPaths(absoluteStaged, absoluteTarget, pendingBackup, backupPath, absoluteJournal, absoluteJournal+".previous") {
		return "", errors.New("replacement paths collide with reserved state")
	}
	for _, reserved := range []string{pendingBackup, backupPath} {
		if _, err := regularFile(reserved); err == nil {
			if reserved == pendingBackup {
				return "", errors.New("pending replacement backup exists without a journal")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	candidateFile, err := os.CreateTemp(targetDirectory, ".xendfile-candidate-*")
	if err != nil {
		return "", fmt.Errorf("create replacement candidate: %w", err)
	}
	candidatePath := candidateFile.Name()
	if !distinctPaths(candidatePath, absoluteStaged, absoluteTarget, pendingBackup, backupPath, absoluteJournal, absoluteJournal+".previous") {
		_ = candidateFile.Close()
		_ = os.Remove(candidatePath)
		return "", errors.New("replacement candidate collides with reserved state")
	}
	keepCandidate := true
	defer func() {
		_ = candidateFile.Close()
		if keepCandidate {
			_ = os.Remove(candidatePath)
		}
	}()
	stagedFile, err := os.Open(absoluteStaged)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	written, copyErr := copyAndHash(candidateFile, stagedFile, hasher, expectedSize)
	closeStagedErr := stagedFile.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeStagedErr != nil {
		return "", closeStagedErr
	}
	if written != expectedSize || hex.EncodeToString(hasher.Sum(nil)) != expectedSHA256 {
		return "", errors.New("replacement candidate does not match signed metadata")
	}
	if err := candidateFile.Chmod(targetInfo.Mode().Perm()); err != nil {
		return "", err
	}
	if err := candidateFile.Sync(); err != nil {
		return "", err
	}
	if err := candidateFile.Close(); err != nil {
		return "", err
	}
	journal := replacementJournal{
		SchemaVersion: 1, Phase: replacementPrepared,
		TargetPath: absoluteTarget, CandidatePath: candidatePath,
		PendingBackupPath: pendingBackup, BackupPath: backupPath,
		PreviousSize: previousSize, PreviousSHA256: previousDigest,
		CandidateSize: expectedSize, CandidateSHA256: expectedSHA256,
	}
	if err := writeReplacementJournal(absoluteJournal, &journal); err != nil {
		return "", err
	}
	keepCandidate = false
	if err := os.Rename(absoluteTarget, pendingBackup); err != nil {
		_ = recoverReplacement(absoluteJournal)
		return "", fmt.Errorf("preserve working installation: %w", err)
	}
	if err := syncDirectory(targetDirectory); err != nil {
		_ = recoverReplacement(absoluteJournal)
		return "", err
	}
	journal.Phase = replacementTargetMoved
	if err := writeReplacementJournal(absoluteJournal, &journal); err != nil {
		_ = recoverReplacement(absoluteJournal)
		return "", err
	}
	if err := os.Rename(candidatePath, absoluteTarget); err != nil {
		_ = recoverReplacement(absoluteJournal)
		return "", fmt.Errorf("activate replacement candidate: %w", err)
	}
	if err := syncDirectory(targetDirectory); err != nil {
		_ = recoverReplacement(absoluteJournal)
		return "", err
	}
	journal.Phase = replacementReplaced
	if err := writeReplacementJournal(absoluteJournal, &journal); err != nil {
		_ = recoverReplacement(absoluteJournal)
		return "", err
	}
	if err := healthCheck(absoluteTarget); err != nil {
		if recoveryErr := recoverReplacement(absoluteJournal); recoveryErr != nil {
			return "", fmt.Errorf("replacement health check failed: %v; rollback failed: %w", err, recoveryErr)
		}
		return "", fmt.Errorf("replacement health check failed and was rolled back: %w", err)
	}
	journal.Phase = replacementHealthy
	if err := writeReplacementJournal(absoluteJournal, &journal); err != nil {
		_ = recoverReplacement(absoluteJournal)
		return "", err
	}
	if err := finalizeHealthyReplacement(&journal, absoluteJournal); err != nil {
		return "", err
	}
	return backupPath, nil
}

func writeReplacementJournal(journalPath string, journal *replacementJournal) error {
	journal.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := validateReplacementJournal(journal, journalPath); err != nil {
		return err
	}
	return writeAtomicPrivateJSON(journalPath, journal)
}

func recoverReplacement(journalPath string) error {
	var journal replacementJournal
	exists, err := readPrivateJSONFile(journalPath, &journal)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := validateReplacementJournal(&journal, journalPath); err != nil {
		return err
	}
	if journal.Phase == replacementHealthy {
		return finalizeHealthyReplacement(&journal, journalPath)
	}
	return rollbackReplacement(&journal, journalPath)
}

func validateReplacementJournal(journal *replacementJournal, journalPath string) error {
	if journal.SchemaVersion != 1 {
		return errors.New("replacement journal schema is unsupported")
	}
	if journal.Phase != replacementPrepared && journal.Phase != replacementTargetMoved && journal.Phase != replacementReplaced && journal.Phase != replacementHealthy {
		return errors.New("replacement journal phase is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, journal.UpdatedAt); err != nil {
		return errors.New("replacement journal timestamp is invalid")
	}
	paths := []string{journal.TargetPath, journal.CandidatePath, journal.PendingBackupPath, journal.BackupPath}
	seen := make(map[string]struct{}, len(paths))
	for _, filePath := range paths {
		if !filepath.IsAbs(filePath) || filepath.Clean(filePath) != filePath {
			return errors.New("replacement journal paths must be clean and absolute")
		}
		if _, exists := seen[filePath]; exists {
			return errors.New("replacement journal paths must be distinct")
		}
		seen[filePath] = struct{}{}
	}
	targetDirectory := filepath.Dir(journal.TargetPath)
	if filepath.Dir(journal.CandidatePath) != targetDirectory || filepath.Dir(journal.PendingBackupPath) != targetDirectory || filepath.Dir(journal.BackupPath) != targetDirectory {
		return errors.New("replacement files must share the installed file directory")
	}
	if journal.PendingBackupPath != journal.TargetPath+".previous.pending" || journal.BackupPath != journal.TargetPath+".last-working" || !strings.HasPrefix(filepath.Base(journal.CandidatePath), ".xendfile-candidate-") {
		return errors.New("replacement journal contains unexpected internal paths")
	}
	absoluteJournal, err := filepath.Abs(journalPath)
	if err != nil {
		return err
	}
	if filepath.Clean(journalPath) != journalPath || absoluteJournal != journalPath || !distinctPaths(journal.TargetPath, journal.CandidatePath, journal.PendingBackupPath, journal.BackupPath, journalPath, journalPath+".previous") {
		return errors.New("replacement journal path collides with managed files")
	}
	if err := existingPathsAreDistinct(journal.TargetPath, journal.CandidatePath, journal.PendingBackupPath, journal.BackupPath, journalPath, journalPath+".previous"); err != nil {
		return err
	}
	if journal.PreviousSize <= 0 || journal.CandidateSize <= 0 || !isLowerHex(journal.PreviousSHA256, sha256.Size*2) || !isLowerHex(journal.CandidateSHA256, sha256.Size*2) {
		return errors.New("replacement journal hashes or sizes are invalid")
	}
	return nil
}

func rollbackReplacement(journal *replacementJournal, journalPath string) error {
	_, pendingErr := regularFile(journal.PendingBackupPath)
	pendingExists := pendingErr == nil
	if pendingErr != nil && !errors.Is(pendingErr, os.ErrNotExist) {
		return pendingErr
	}
	if pendingExists {
		matches, err := fileMatches(journal.PendingBackupPath, journal.PreviousSize, journal.PreviousSHA256)
		if err != nil {
			return err
		}
		if !matches {
			return errors.New("pending backup does not match the last working installation")
		}
		if _, err := regularFile(journal.TargetPath); err == nil {
			previousMatches, previousErr := fileMatches(journal.TargetPath, journal.PreviousSize, journal.PreviousSHA256)
			candidateMatches, candidateErr := fileMatches(journal.TargetPath, journal.CandidateSize, journal.CandidateSHA256)
			if previousErr != nil || candidateErr != nil || (!previousMatches && !candidateMatches) {
				return errors.New("installed file does not match a journaled replacement generation")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := removeRegularFile(journal.TargetPath); err != nil {
			return err
		}
		if err := os.Rename(journal.PendingBackupPath, journal.TargetPath); err != nil {
			return fmt.Errorf("restore last working installation: %w", err)
		}
		if err := syncDirectory(filepath.Dir(journal.TargetPath)); err != nil {
			return err
		}
	}
	matches, err := fileMatches(journal.TargetPath, journal.PreviousSize, journal.PreviousSHA256)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("cannot prove the last working installation was restored")
	}
	if err := removeVerifiedCandidate(journal); err != nil {
		return err
	}
	return clearPrivateState(journalPath)
}

func finalizeHealthyReplacement(journal *replacementJournal, journalPath string) error {
	matches, err := fileMatches(journal.TargetPath, journal.CandidateSize, journal.CandidateSHA256)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("healthy replacement no longer matches the verified candidate")
	}
	_, pendingErr := regularFile(journal.PendingBackupPath)
	if pendingErr == nil {
		matches, matchErr := fileMatches(journal.PendingBackupPath, journal.PreviousSize, journal.PreviousSHA256)
		if matchErr != nil {
			return matchErr
		}
		if !matches {
			return errors.New("pending backup does not match the last working installation")
		}
		if err := removeRegularFile(journal.BackupPath); err != nil {
			return err
		}
		if err := os.Rename(journal.PendingBackupPath, journal.BackupPath); err != nil {
			return fmt.Errorf("promote last working backup: %w", err)
		}
		if err := syncDirectory(filepath.Dir(journal.TargetPath)); err != nil {
			return err
		}
	} else if !errors.Is(pendingErr, os.ErrNotExist) {
		return pendingErr
	}
	backupMatches, err := fileMatches(journal.BackupPath, journal.PreviousSize, journal.PreviousSHA256)
	if err != nil {
		return err
	}
	if !backupMatches {
		return errors.New("last working backup does not match the replacement journal")
	}
	if err := removeVerifiedCandidate(journal); err != nil {
		return err
	}
	return clearPrivateState(journalPath)
}

func removeVerifiedCandidate(journal *replacementJournal) error {
	if _, err := regularFile(journal.CandidatePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	matches, err := fileMatches(journal.CandidatePath, journal.CandidateSize, journal.CandidateSHA256)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("replacement candidate no longer matches the journal")
	}
	return removeRegularFile(journal.CandidatePath)
}

func distinctPaths(paths ...string) bool {
	seen := make(map[string]struct{}, len(paths))
	for _, filePath := range paths {
		if _, exists := seen[filePath]; exists {
			return false
		}
		seen[filePath] = struct{}{}
	}
	return true
}

func existingPathsAreDistinct(paths ...string) error {
	type existingPath struct {
		path string
		info os.FileInfo
	}
	existing := make([]existingPath, 0, len(paths))
	for _, filePath := range paths {
		info, err := os.Lstat(filePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, prior := range existing {
			if os.SameFile(prior.info, info) {
				return fmt.Errorf("replacement paths %q and %q identify the same file", prior.path, filePath)
			}
		}
		existing = append(existing, existingPath{path: filePath, info: info})
	}
	return nil
}

func fileMatches(filePath string, expectedSize int64, expectedSHA256 string) (bool, error) {
	size, digest, err := fileSizeAndSHA256(filePath)
	if err != nil {
		return false, err
	}
	return size == expectedSize && digest == expectedSHA256, nil
}

func fileSizeAndSHA256(filePath string) (int64, string, error) {
	before, err := regularFile(filePath)
	if err != nil {
		return 0, "", err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return 0, "", err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return 0, "", err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = file.Close()
		return 0, "", errors.New("file changed while it was opened")
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil {
		return 0, "", copyErr
	}
	if closeErr != nil {
		return 0, "", closeErr
	}
	after, err := regularFile(filePath)
	if err != nil || !os.SameFile(before, after) || written != before.Size() || written != after.Size() {
		return 0, "", errors.New("file changed while it was hashed")
	}
	return written, hex.EncodeToString(hasher.Sum(nil)), nil
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

type semanticVersion [3]uint64

func parseVersion(value string) (semanticVersion, error) {
	var result semanticVersion
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return result, errors.New("version must contain major.minor.patch")
	}
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return result, errors.New("version component is not canonical")
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return result, errors.New("version component is not numeric")
			}
		}
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return result, errors.New("version component is too large")
		}
		result[index] = parsed
	}
	return result, nil
}

func compareVersion(left, right string) int {
	a, errA := parseVersion(left)
	b, errB := parseVersion(right)
	if errA != nil || errB != nil {
		return -2
	}
	for index := range a {
		if a[index] < b[index] {
			return -1
		}
		if a[index] > b[index] {
			return 1
		}
	}
	return 0
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}
