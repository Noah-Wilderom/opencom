package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLookupChecksum_FindsExactMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "checksums.txt")
	body := []byte(
		"deadbeef  opencom_0.1.13_Linux_x86_64.tar.gz\n" +
			"feedface  opencom_0.1.13_Darwin_arm64.tar.gz\n",
	)
	assert.NoError(t, os.WriteFile(path, body, 0o600))

	got, err := lookupChecksum(path, "opencom_0.1.13_Darwin_arm64.tar.gz")
	assert.NoError(t, err)
	assert.Equal(t, "feedface", got)
}

func TestLookupChecksum_AcceptsLeadingStar(t *testing.T) {
	// sha256sum --binary uses "*filename" — accept either form.
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "checksums.txt")
	body := []byte("aabbcc  *opencom_0.1.13_Linux_x86_64.tar.gz\n")
	assert.NoError(t, os.WriteFile(path, body, 0o600))

	got, err := lookupChecksum(path, "opencom_0.1.13_Linux_x86_64.tar.gz")
	assert.NoError(t, err)
	assert.Equal(t, "aabbcc", got)
}

func TestLookupChecksum_MissingEntryErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "checksums.txt")
	assert.NoError(t, os.WriteFile(path, []byte("xx  other.tar.gz\n"), 0o600))

	_, err := lookupChecksum(path, "opencom_0.1.13_Linux_x86_64.tar.gz")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no checksum entry for")
}

func TestExtractBinary_PullsBinaryFromTarball(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Build a tar.gz mimicking the release layout:
	//   opencom_0.1.13_Linux_x86_64/opencom
	//   opencom_0.1.13_Linux_x86_64/LICENSE
	tarballPath := filepath.Join(dir, "opencom.tar.gz")
	wantPayload := []byte("\x7fELF...fake binary contents")
	writeTarball(t, tarballPath, map[string][]byte{
		"opencom_0.1.13_Linux_x86_64/opencom": wantPayload,
		"opencom_0.1.13_Linux_x86_64/LICENSE": []byte("license text"),
	})

	dest := filepath.Join(dir, "extracted")
	assert.NoError(t, extractBinary(tarballPath, "opencom", dest))

	got, err := os.ReadFile(dest)
	assert.NoError(t, err)
	assert.Equal(t, wantPayload, got)
}

func TestExtractBinary_MissingBinaryErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tarballPath := filepath.Join(dir, "opencom.tar.gz")
	writeTarball(t, tarballPath, map[string][]byte{
		"opencom_0.1.13_Linux_x86_64/LICENSE": []byte("license text"),
	})

	err := extractBinary(tarballPath, "opencom", filepath.Join(dir, "out"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in tarball")
}

func TestPlatformAssetParts_KnownTriples(t *testing.T) {
	// We can't override runtime.GOOS at runtime, so the assertion
	// is shape-only: returns non-empty for the running platform.
	t.Parallel()
	osTitle, arch, ext, err := platformAssetParts()
	assert.NoError(t, err)
	assert.NotEmpty(t, osTitle)
	assert.NotEmpty(t, arch)
	assert.Contains(t, []string{"tar.gz", "zip"}, ext)
}

func TestSha256File_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	body := []byte("hello world")
	assert.NoError(t, os.WriteFile(path, body, 0o600))

	got, err := sha256File(path)
	assert.NoError(t, err)
	want := sha256.Sum256(body)
	assert.Equal(t, hex.EncodeToString(want[:]), got)
}

func TestReplaceBinary_OverwritesAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exePath := filepath.Join(dir, "opencom")
	srcPath := filepath.Join(dir, "src")
	assert.NoError(t, os.WriteFile(exePath, []byte("OLD"), 0o755))
	assert.NoError(t, os.WriteFile(srcPath, []byte("NEW"), 0o755))

	assert.NoError(t, replaceBinary(srcPath, exePath))

	got, err := os.ReadFile(exePath)
	assert.NoError(t, err)
	assert.Equal(t, []byte("NEW"), got)

	info, err := os.Stat(exePath)
	assert.NoError(t, err)
	// Mode should be 0755 (executable).
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func writeTarball(t *testing.T, dest string, entries map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		assert.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}))
		_, err := tw.Write(body)
		assert.NoError(t, err)
	}
	assert.NoError(t, tw.Close())
	assert.NoError(t, gz.Close())
	assert.NoError(t, os.WriteFile(dest, buf.Bytes(), 0o600))
}
