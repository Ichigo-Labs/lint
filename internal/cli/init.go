package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// repoSlug is the GitHub owner/name releases are downloaded from.
const repoSlug = "Ichigo-Labs/lint"

// pseudoVersionRE matches the "<14-digit timestamp>-<12-hex commit>" core Go
// puts in module pseudo-versions (e.g. from `go install ...@latest` when no
// clean release tag resolves) — in all three forms: vX.0.0-<ts>-<hash>,
// vX.Y.Z-0.<ts>-<hash>, and vX.Y.Z-pre.0.<ts>-<hash>. Such versions have no
// release asset. The timestamp is preceded by '-' (first form) or '.' (others).
var pseudoVersionRE = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}`)

// isReleaseTag reports whether v names a downloadable GitHub release tag — a
// clean semver tag like v0.2.0 or v0.2.0-rc1 — rather than a pseudo-version.
func isReleaseTag(v string) bool {
	return strings.HasPrefix(v, "v") && !pseudoVersionRE.MatchString(v)
}

func newInitCmd() *cobra.Command {
	var dir, ver string
	var noHook, force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Vendor a pinned lint into ./bin and add a pre-commit hook",
		Long: `init sets up a project to use a pinned version of lint without a global install.

It writes a small launcher (bin/lint) that downloads the pinned release for the
current OS/arch on first use, verifies its checksum, caches it under bin/.lint/,
and runs it. Commit bin/lint; teammates on Linux/macOS then run './bin/lint' and
the right binary is fetched automatically. (The launcher targets Linux and
macOS; on Windows, install lint from the releases page.)

init also adds bin/.lint/ to .gitignore and installs a git pre-commit hook that
runs 'lint check' on staged files (unless one already exists).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case ver == "":
				ver = readBuildInfo().Version
				if !isReleaseTag(ver) {
					ver = "latest"
					fmt.Fprintln(os.Stderr, "lint init: pinning to 'latest' (no release tag for this build); pass --version vX.Y.Z to pin a release")
				}
			case ver != "latest" && !strings.HasPrefix(ver, "v"):
				// Release tags are v-prefixed (release.yml: tags ["v*"]); a bare
				// "0.2.0" would 404. Normalize so --version 0.2.0 == v0.2.0.
				fmt.Fprintf(os.Stderr, "lint init: normalizing --version %q to v%s\n", ver, ver)
				ver = "v" + ver
			}

			launcher := filepath.Join(dir, "lint")
			if err := writeLauncher(launcher, ver, force); err != nil {
				return err
			}

			cacheIgnore := filepath.ToSlash(filepath.Join(dir, ".lint")) + "/"
			if err := ensureGitignore(cacheIgnore); err != nil {
				return fmt.Errorf("updating .gitignore: %w", err)
			}

			// Warm the cache for this platform so lint is ready immediately.
			// Best effort: if no release exists yet (or we're offline), the
			// launcher fetches it on first real use. The launcher is a bash
			// script, so it can't be exec'd directly on Windows.
			if runtime.GOOS == "windows" {
				fmt.Fprintln(os.Stderr, "note: the vendored launcher targets Linux/macOS; on Windows install lint from https://github.com/"+repoSlug+"/releases (.zip)")
			} else {
				fmt.Println("downloading lint for this platform...")
				warm := exec.Command(launcher, "--version")
				warm.Stdout, warm.Stderr = os.Stdout, os.Stderr
				if err := warm.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "note: warm-up run of %s failed (%v); it will fetch lint on first use\n", filepath.ToSlash(launcher), err)
				}
			}

			if !noHook {
				if err := installPreCommitHook(dir); err != nil {
					fmt.Fprintf(os.Stderr, "note: skipped pre-commit hook: %v\n", err)
				}
			}

			runPath := filepath.ToSlash(launcher)
			if !filepath.IsAbs(launcher) {
				runPath = "./" + runPath
			}
			fmt.Printf("\nDone. Commit %s, then run '%s check'.\n", launcher, runPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "bin", "directory to vendor the launcher into")
	cmd.Flags().StringVar(&ver, "version", "", "release tag to pin (default: this lint's version, else latest)")
	cmd.Flags().BoolVar(&noHook, "no-hook", false, "do not install a git pre-commit hook")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing launcher")
	return cmd
}

// writeLauncher writes the bin/lint launcher script, pinned to ver.
func writeLauncher(path, ver string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		fmt.Fprintf(os.Stderr, "lint init: %s already exists; use --force to overwrite\n", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := strings.NewReplacer("__VERSION__", ver, "__REPO__", repoSlug).Replace(launcherTemplate)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Printf("wrote %s (pinned to %s)\n", path, ver)
	return nil
}

// ensureGitignore appends entry to .gitignore unless it's already listed.
func ensureGitignore(entry string) error {
	const path = ".gitignore"
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	if _, err := fmt.Fprintf(f, "%s# lint vendored-binary cache\n%s\n", prefix, entry); err != nil {
		return err
	}
	fmt.Printf("added %s to .gitignore\n", entry)
	return nil
}

// installPreCommitHook writes a pre-commit hook unless one already exists.
func installPreCommitHook(dir string) error {
	out, err := exec.Command("git", "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		return fmt.Errorf("locating git hooks dir (is this a git repo with git installed?): %w", err)
	}
	hooksDir := strings.TrimSpace(string(out))
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}
	hook := filepath.Join(hooksDir, "pre-commit")
	if _, err := os.Stat(hook); err == nil {
		fmt.Fprintf(os.Stderr, "lint init: %s already exists; leaving it untouched\n", hook)
		return nil
	}

	// Git runs hooks with CWD = the worktree root, but init may be run from a
	// subdirectory, so anchor the launcher reference to that root.
	launcherPath := filepath.Join(dir, "lint")
	var ref string
	if filepath.IsAbs(launcherPath) {
		ref = filepath.ToSlash(launcherPath)
	} else {
		prefixOut, perr := exec.Command("git", "rev-parse", "--show-prefix").Output()
		if perr != nil {
			return fmt.Errorf("resolving repo-relative path: %w", perr)
		}
		prefix := strings.TrimSpace(string(prefixOut)) // "packages/app/" in a subdir; "" at root
		ref = "./" + filepath.ToSlash(filepath.Join(prefix, launcherPath))
	}

	content := strings.ReplaceAll(hookTemplate, "__LAUNCHER__", ref)
	if err := os.WriteFile(hook, []byte(content), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(hook, 0o755); err != nil {
		return err
	}
	fmt.Printf("installed git pre-commit hook at %s\n", hook)
	return nil
}

// launcherTemplate is the bin/lint launcher. __VERSION__ / __REPO__ are
// substituted at write time. It downloads the pinned release for the current
// OS/arch on first use, verifies the checksum, caches it, and execs it.
const launcherTemplate = `#!/usr/bin/env bash
#
# Vendored lint launcher (generated by 'lint init').
#
# Runs a pinned lint release, downloading the right build for this OS/arch on
# first use and caching it under the directory next to this script. Commit this
# file so the whole team runs the same version without a global install.
#
# Targets Linux and macOS; on Windows, install lint from the releases page.
# Pinned below; override per-run with LINT_VERSION=vX.Y.Z. Re-pin with 'lint init'.
#
set -euo pipefail

REPO="__REPO__"
VERSION="${LINT_VERSION:-__VERSION__}"

here="$(cd "$(dirname "$0")" && pwd)"
cache="${here}/.lint"

os="$(uname -s)"
case "$os" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  MINGW* | MSYS* | CYGWIN*)
    echo "lint: the vendored launcher supports Linux and macOS; on Windows install lint from https://github.com/${REPO}/releases (.zip)" >&2
    exit 1 ;;
  *) echo "lint: unsupported OS '$os'; install from https://github.com/${REPO}" >&2; exit 1 ;;
esac
arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64)  arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "lint: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

bin="${cache}/${VERSION}/${os}_${arch}/lint"

if [ ! -x "$bin" ]; then
  asset="lint_${os}_${arch}.tar.gz"
  if [ "$VERSION" = "latest" ]; then
    base="https://github.com/${REPO}/releases/latest/download"
  else
    base="https://github.com/${REPO}/releases/download/${VERSION}"
  fi

  if command -v curl >/dev/null 2>&1; then
    dl() { curl -fSL --retry 3 "$1" -o "$2"; }
  elif command -v wget >/dev/null 2>&1; then
    dl() { wget -q "$1" -O "$2"; }
  else
    echo "lint: need curl or wget to download lint" >&2; exit 1
  fi

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  echo "lint: downloading ${asset} (${VERSION})..." >&2
  dl "${base}/${asset}" "${tmp}/${asset}" || { echo "lint: download failed: ${base}/${asset}" >&2; exit 1; }
  dl "${base}/checksums.txt" "${tmp}/checksums.txt" || { echo "lint: could not fetch checksums.txt" >&2; exit 1; }

  want="$(awk -v f="$asset" '$2 == f {print $1}' "${tmp}/checksums.txt" | head -n1)"
  [ -n "$want" ] || { echo "lint: no checksum listed for ${asset}" >&2; exit 1; }
  if command -v sha256sum >/dev/null 2>&1; then
    got="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    got="$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')"
  else
    echo "lint: need sha256sum or shasum to verify the download" >&2; exit 1
  fi
  [ "$want" = "$got" ] || { echo "lint: checksum mismatch for ${asset}" >&2; exit 1; }

  tar -xzf "${tmp}/${asset}" -C "$tmp" lint || { echo "lint: failed to extract lint" >&2; exit 1; }
  chmod +x "${tmp}/lint"

  # Install atomically and concurrency-safely: stage to a per-process unique
  # name on the same filesystem as the cache, then rename. An interrupted or
  # racing run can never leave a partial binary at "$bin".
  bindir="${cache}/${VERSION}/${os}_${arch}"
  mkdir -p "$bindir"
  staging="${bindir}/.lint.$$.tmp"
  trap 'rm -rf "$tmp"; rm -f "$staging"' EXIT
  cp "${tmp}/lint" "$staging"
  mv -f "$staging" "$bin"

  # exec (below) replaces this shell, so the EXIT trap won't fire — clean up now.
  rm -rf "$tmp"
  trap - EXIT
fi

exec "$bin" "$@"
`

// hookTemplate is the git pre-commit hook. __LAUNCHER__ is substituted with the
// path to the vendored launcher (relative to the worktree root).
const hookTemplate = `#!/usr/bin/env bash
#
# git pre-commit hook installed by 'lint init'. Lints staged files before each
# commit. Delete this file to disable, or edit the lint command below.
#
set -euo pipefail

files="$(git diff --cached --name-only --diff-filter=ACM)"
[ -z "$files" ] && exit 0

# Prefer the vendored launcher when it actually runs, else a lint on PATH. If
# neither is available (not downloaded yet, offline, or unsupported OS), skip
# rather than block the commit on a tooling failure.
if [ -x "__LAUNCHER__" ] && "__LAUNCHER__" --version >/dev/null 2>&1; then
  runner="__LAUNCHER__"
elif command -v lint >/dev/null 2>&1; then
  runner="lint"
else
  echo "lint: skipping pre-commit check (lint is not available yet)" >&2
  exit 0
fi

# Add --error-on-warning to also block commits on warning-severity findings.
git diff --cached --name-only --diff-filter=ACM -z | xargs -0 "$runner" check
`
