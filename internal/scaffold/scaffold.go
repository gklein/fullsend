package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:fullsend-repo
var content embed.FS

// FullsendRepoFile returns the content of a file from the fullsend-repo scaffold.
// The path is relative to the fullsend-repo root (e.g., ".github/workflows/triage.yml").
func FullsendRepoFile(path string) ([]byte, error) {
	return content.ReadFile("fullsend-repo/" + path)
}

// executableFiles lists scaffold paths committed with mode 100755.
// embed.FS does not preserve permission bits, so we track them here.
// TestFileModeMatchesFilesystem verifies this set stays in sync.
var executableFiles = map[string]struct{}{
	"scripts/post-code.sh":                   {},
	"scripts/post-review.sh":                 {},
	"scripts/post-triage.sh":                 {},
	"scripts/post-triage-test.sh":            {},
	"scripts/pre-code.sh":                    {},
	"scripts/pre-review.sh":                  {},
	"scripts/pre-triage.sh":                  {},
	"scripts/prepare-sandbox-credentials.sh": {},
	"scripts/reconcile-repos.sh":             {},
	"scripts/scan-secrets":                   {},
	"scripts/validate-output-schema.sh":      {},
	"scripts/validate-output-schema-test.sh": {},
}

// FileMode returns the Git tree mode for a scaffold file.
func FileMode(path string) string {
	if _, ok := executableFiles[path]; ok {
		return "100755"
	}
	return "100644"
}

// WalkFullsendRepo calls fn for each file in the fullsend-repo scaffold.
// Paths passed to fn are relative to the fullsend-repo root.
func WalkFullsendRepo(fn func(path string, content []byte) error) error {
	return fs.WalkDir(content, "fullsend-repo", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := content.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", path, readErr)
		}
		// Strip the "fullsend-repo/" prefix so callers get repo-relative paths.
		relPath := path[len("fullsend-repo/"):]
		return fn(relPath, data)
	})
}
