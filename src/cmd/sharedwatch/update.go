package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// handleUpdate implements the `update` subcommand.
//
// Safe by default: prints what would change and exits without touching disk.
// Pass --apply to actually install. The flow when --apply is set:
//  1. Pre-check the running binary's directory is writable by the current
//     user (no sudo escalation).
//  2. Resolve target version (default: latest GitHub release).
//  3. Download tarball + manifest.yaml; verify the tarball's SHA-256 matches
//     the manifest entry. Refuse to install if either step fails.
//  4. Extract the embedded `sharedwatch` binary into a temp dir.
//  5. Smoke-test the new binary by running it with `version`.
//  6. Atomic-rename it over the current binary path.
//
// Updating while a daemon is running is safe on POSIX: the running process
// holds the old inode (rename(2) on Linux/macOS unlinks but does not invalidate
// open handles). The new binary only takes effect when the daemon is restarted.
func handleUpdate(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	apply := fs.Bool("apply", false, "actually perform the update (default: check only, no changes)")
	wantVersion := fs.String("version", "", "target version tag (default: latest release on GitHub)")
	repo := fs.String("repo", "ab0t/sharedwatch", "GitHub repo to fetch from")
	assumeYes := fs.Bool("yes", false, "skip the interactive confirmation when --apply")
	timeout := fs.Duration("timeout", 60*time.Second, "network operation timeout")
	_ = fs.Parse(args)

	cur := Version

	binPath, err := os.Executable()
	if err != nil {
		fatal(fmt.Errorf("locate own binary: %w", err))
	}
	if resolved, e := filepath.EvalSymlinks(binPath); e == nil {
		binPath = resolved
	}

	httpCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	target := *wantVersion
	if target == "" {
		latest, err := fetchLatestTag(httpCtx, *repo)
		if err != nil {
			fatal(fmt.Errorf("fetch latest release tag from github.com/%s: %w", *repo, err))
		}
		target = latest
	}

	platform := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	tarball := fmt.Sprintf("sharedwatch_%s_%s.tar.gz", strings.TrimPrefix(target, "v"), platform)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", *repo, target)
	tarURL := base + "/" + tarball
	manifestURL := base + "/manifest.yaml"

	fmt.Println("current version: ", cur)
	fmt.Println("target version:  ", target)
	fmt.Println("repo:            ", *repo)
	fmt.Println("platform:        ", platform)
	fmt.Println("binary path:     ", binPath)
	fmt.Println("download:        ", tarURL)
	fmt.Println("manifest:        ", manifestURL)

	if target == cur && cur != "dev" {
		fmt.Println()
		fmt.Println("already on", cur, "— no update needed")
		return
	}

	if !*apply {
		fmt.Println()
		fmt.Println("DRY RUN — no changes made. To apply: sharedwatch update --apply")
		return
	}

	if err := checkBinaryWritable(binPath); err != nil {
		fatal(fmt.Errorf("cannot write to %s: %w (cowardly refusing to escalate; run from a path you own)", filepath.Dir(binPath), err))
	}

	if !*assumeYes {
		fmt.Println()
		fmt.Printf("Replace %s with %s ? [y/N] ", binPath, target)
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		reply := strings.ToLower(strings.TrimSpace(line))
		if reply != "y" && reply != "yes" {
			fmt.Println("aborted")
			return
		}
	}

	tmpDir, err := os.MkdirTemp("", "sharedwatch-update-*")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	manifestPath := filepath.Join(tmpDir, "manifest.yaml")
	tarPath := filepath.Join(tmpDir, tarball)

	fmt.Println("downloading manifest...")
	if err := downloadFile(httpCtx, manifestURL, manifestPath); err != nil {
		fatal(fmt.Errorf("download manifest: %w", err))
	}
	fmt.Println("downloading tarball (~few MB)...")
	if err := downloadFile(httpCtx, tarURL, tarPath); err != nil {
		fatal(fmt.Errorf("download tarball: %w", err))
	}

	fmt.Println("verifying SHA-256...")
	expected, err := manifestSHA(manifestPath, tarball)
	if err != nil {
		fatal(fmt.Errorf("read manifest: %w", err))
	}
	actual, err := fileSHA(tarPath)
	if err != nil {
		fatal(err)
	}
	if expected != actual {
		fatal(fmt.Errorf("SHA-256 mismatch — expected %s, got %s (refusing to install)", expected, actual))
	}
	fmt.Println("  expected:", expected)
	fmt.Println("  actual:  ", actual, "OK")

	fmt.Println("extracting binary...")
	stagedBin, err := extractBinary(tarPath, tmpDir)
	if err != nil {
		fatal(err)
	}
	if err := os.Chmod(stagedBin, 0o755); err != nil {
		fatal(err)
	}

	fmt.Println("smoke-testing new binary...")
	if err := smokeNewBinary(stagedBin); err != nil {
		fatal(fmt.Errorf("downloaded binary failed smoke test: %w (NOT installing)", err))
	}

	stagedPath := binPath + ".new"
	if err := copyFile(stagedBin, stagedPath, 0o755); err != nil {
		fatal(fmt.Errorf("stage new binary at %s: %w", stagedPath, err))
	}
	if err := os.Rename(stagedPath, binPath); err != nil {
		_ = os.Remove(stagedPath)
		fatal(fmt.Errorf("install new binary at %s: %w", binPath, err))
	}

	fmt.Println()
	fmt.Println("installed", target, "to", binPath)
	fmt.Println("if a daemon is running, restart it to pick up the new binary")
}

func fetchLatestTag(ctx context.Context, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sharedwatch-update/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return "", fmt.Errorf("github api returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.TagName == "" {
		return "", errors.New("github api returned no tag_name (no releases yet?)")
	}
	return out.TagName, nil
}

func downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sharedwatch-update/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s -> HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func fileSHA(path string) (string, error) {
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

// manifestSHA finds the SHA-256 of a given file in a release manifest.
// Manifest format (from scripts/release.sh):
//
//	artifacts:
//	  - file: <name>
//	    sha256: <hex>
//	    size_bytes: <int>
//
// We grep the file line then the next sha256 within a small window — no
// YAML parser dependency.
func manifestSHA(manifestPath, fileName string) (string, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	target := "file: " + fileName
	for i, ln := range lines {
		if strings.Contains(ln, target) {
			for j := i + 1; j < len(lines) && j < i+5; j++ {
				s := strings.TrimSpace(lines[j])
				if strings.HasPrefix(s, "sha256:") {
					return strings.TrimSpace(strings.TrimPrefix(s, "sha256:")), nil
				}
			}
		}
	}
	return "", fmt.Errorf("no entry for %s in manifest", fileName)
}

func extractBinary(tarPath, dstDir string) (string, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		base := filepath.Base(hdr.Name)
		if base != "sharedwatch" {
			continue
		}
		dst := filepath.Join(dstDir, "sharedwatch")
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", errors.New("tarball did not contain a 'sharedwatch' binary")
}

func smokeNewBinary(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(out)), "sharedwatch") {
		return fmt.Errorf("unexpected output: %q", string(out))
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func checkBinaryWritable(binPath string) error {
	dir := filepath.Dir(binPath)
	sentinel := filepath.Join(dir, ".sharedwatch-update-test")
	f, err := os.Create(sentinel)
	if err != nil {
		return err
	}
	_ = f.Close()
	_ = os.Remove(sentinel)
	return nil
}
