package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"opencom/internal/version"
)

func newUpgradeCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Download and install the latest opencom release",
		Long: `Resolves the running binary path, downloads the matching
release tarball from GitHub, verifies its SHA256 checksum, and
atomically replaces the binary in place.

Skipped on dev builds (no released version to upgrade to). Pass
--force to upgrade anyway when the binary reports its version as
"dev" or as the same version as the latest release.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cmd, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"upgrade even if the running binary already matches latest (or is a dev build)")
	return cmd
}

func runUpgrade(cmd *cobra.Command, force bool) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()

	if Version == "dev" && !force {
		return errors.New("running a dev build (no released version to upgrade to); pass --force to install latest anyway")
	}

	// 1. Resolve current binary path. os.Executable resolves symlinks
	//    on Linux/macOS; on Windows it returns the path with the .exe
	//    extension. The atomic-rename below depends on writing a temp
	//    file in the same directory.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving current binary path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolving symlinks on %s: %w", exePath, err)
	}

	// 2. Resolve the matching release asset.
	asset, err := resolveReleaseAsset(ctx, version.DefaultRepo)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Latest release: %s\n", asset.tag)
	if !force && strings.TrimPrefix(asset.tag, "v") == strings.TrimPrefix(Version, "v") {
		fmt.Fprintln(out, "Already running the latest release. Pass --force to reinstall.")
		return nil
	}

	// 3. Download the tarball + checksums.txt to a temp dir.
	tmpDir, err := os.MkdirTemp("", "opencom-upgrade-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tarballPath := filepath.Join(tmpDir, asset.tarballName)
	fmt.Fprintf(out, "Downloading %s...\n", asset.tarballName)
	if err := download(ctx, asset.tarballURL, tarballPath); err != nil {
		return fmt.Errorf("downloading tarball: %w", err)
	}

	// 4. Verify checksum. Mandatory — protects against MITM and
	//    half-released artifacts (the checksums file is generated
	//    after every binary in the matrix completes successfully).
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := download(ctx, asset.checksumsURL, checksumsPath); err != nil {
		return fmt.Errorf("downloading checksums: %w", err)
	}
	expected, err := lookupChecksum(checksumsPath, asset.tarballName)
	if err != nil {
		return err
	}
	got, err := sha256File(tarballPath)
	if err != nil {
		return fmt.Errorf("hashing tarball: %w", err)
	}
	if got != expected {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s",
			asset.tarballName, got, expected)
	}
	fmt.Fprintln(out, "Checksum verified.")

	// 5. Extract the binary from the tarball into tmpDir.
	binName := "opencom"
	if runtime.GOOS == "windows" {
		binName = "opencom.exe"
	}
	newBinPath := filepath.Join(tmpDir, binName)
	if err := extractBinary(tarballPath, binName, newBinPath); err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}
	if err := os.Chmod(newBinPath, 0o755); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}

	// 6. Replace the running binary. Atomic via rename if same
	//    filesystem; copy+rename across filesystems. On the active
	//    process's binary, this works on Linux/macOS (the kernel
	//    holds the old inode open until the process exits) and on
	//    Windows (file is mapped, but rename succeeds since v2016+).
	if err := replaceBinary(newBinPath, exePath); err != nil {
		return fmt.Errorf("replacing binary at %s: %w", exePath, err)
	}

	fmt.Fprintf(out, "Installed v%s at %s\n", strings.TrimPrefix(asset.tag, "v"), exePath)
	fmt.Fprintln(out, "If a daemon is running, restart it: opencom daemon stop && opencom daemon start --background")
	return nil
}

// releaseAsset bundles the URLs and filenames opencom needs for one
// upgrade run.
type releaseAsset struct {
	tag          string // e.g. "v0.1.13"
	tarballName  string // e.g. "opencom_0.1.13_Linux_x86_64.tar.gz"
	tarballURL   string
	checksumsURL string
}

// resolveReleaseAsset hits the GitHub releases API to find the latest
// tag, then constructs the URLs/filenames for the running platform.
// Naming matches the matrix release workflow:
//
//	opencom_<version>_<OS_TITLE>_<ARCH>.{tar.gz,zip}
//
// where OS_TITLE is "Linux", "Darwin", or "Windows" and ARCH is
// "x86_64" or "arm64".
func resolveReleaseAsset(ctx context.Context, repo string) (releaseAsset, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return releaseAsset{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return releaseAsset{}, fmt.Errorf("fetching release info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return releaseAsset{}, fmt.Errorf("releases API returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return releaseAsset{}, fmt.Errorf("decoding release info: %w", err)
	}
	if payload.TagName == "" {
		return releaseAsset{}, errors.New("releases API returned no tag")
	}

	osTitle, arch, ext, err := platformAssetParts()
	if err != nil {
		return releaseAsset{}, err
	}
	v := strings.TrimPrefix(payload.TagName, "v")
	wantTarball := fmt.Sprintf("opencom_%s_%s_%s.%s", v, osTitle, arch, ext)
	wantChecksums := "checksums.txt"

	a := releaseAsset{tag: payload.TagName, tarballName: wantTarball}
	for _, asset := range payload.Assets {
		switch asset.Name {
		case wantTarball:
			a.tarballURL = asset.DownloadURL
		case wantChecksums:
			a.checksumsURL = asset.DownloadURL
		}
	}
	if a.tarballURL == "" {
		return releaseAsset{}, fmt.Errorf("no release asset matching %s found in %s",
			wantTarball, payload.TagName)
	}
	if a.checksumsURL == "" {
		return releaseAsset{}, fmt.Errorf("no checksums.txt asset in release %s", payload.TagName)
	}
	return a, nil
}

// platformAssetParts maps GOOS/GOARCH to the (OS_TITLE, ARCH, ext)
// triple the release workflow uses to name artifacts.
func platformAssetParts() (osTitle, arch, ext string, err error) {
	switch runtime.GOOS {
	case "linux":
		osTitle = "Linux"
		ext = "tar.gz"
	case "darwin":
		osTitle = "Darwin"
		ext = "tar.gz"
	case "windows":
		osTitle = "Windows"
		ext = "zip"
	default:
		return "", "", "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "arm64"
	default:
		return "", "", "", fmt.Errorf("unsupported arch: %s", runtime.GOARCH)
	}
	return osTitle, arch, ext, nil
}

// download streams the URL into dest. Bounded retries are intentionally
// omitted; one failure aborts the upgrade — the user can re-run.
func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// lookupChecksum reads a goreleaser/sha256sum-style checksums file and
// returns the hex digest for filename, or an error if not present.
func lookupChecksum(path, filename string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == filename || fields[1] == "*"+filename {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum entry for %s", filename)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary pulls the file named binName out of a tar.gz archive
// and writes it to dest. The release tarballs are structured as
// <archive-name>/<binary>, so we accept any path whose basename matches.
func extractBinary(tarballPath, binName, dest string) error {
	f, err := os.Open(tarballPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("binary %s not found in tarball", binName)
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) != binName {
			continue
		}
		out, err := os.Create(dest)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}

// replaceBinary atomically replaces the running binary at exePath
// with the new one at src. Uses os.Rename when both are on the same
// filesystem (atomic), falls back to copy+rename otherwise.
func replaceBinary(src, exePath string) error {
	dir := filepath.Dir(exePath)
	tmp := filepath.Join(dir, ".opencom-new-"+filepath.Base(exePath))
	// Copy src into the destination directory first, then rename
	// over exePath. This pattern works even when src is on a
	// different filesystem (the os.MkdirTemp default).
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exePath); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
