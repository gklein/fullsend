package layers

import (
	"bytes"
	"context"
	"fmt"

	"github.com/fullsend-ai/fullsend/internal/forge"
	"github.com/fullsend-ai/fullsend/internal/scaffold"
	"github.com/fullsend-ai/fullsend/internal/ui"
)

const codeownersPath = "CODEOWNERS"

// managedFiles lists every file this layer manages.
// Populated at init from the scaffold plus the CODEOWNERS sentinel.
var managedFiles []string

func init() {
	if err := scaffold.WalkFullsendRepo(func(path string, _ []byte) error {
		managedFiles = append(managedFiles, path)
		return nil
	}); err != nil {
		panic(fmt.Sprintf("walking scaffold: %v", err))
	}
	managedFiles = append(managedFiles, codeownersPath)
}

// actionYMLPath is the repo-relative path to the composite action that
// contains the CLI version input default.
const actionYMLPath = ".github/actions/fullsend/action.yml"

// versionDefault is the placeholder in the embedded action.yml that gets
// replaced with the installing CLI's version.
var versionDefault = []byte("    default: latest")

// WorkflowsLayer manages workflow files and CODEOWNERS in the .fullsend
// config repo. It writes the reusable agent dispatch workflow, the repo
// onboarding workflow, and a CODEOWNERS file that grants the installing
// user ownership of all config-repo contents.
type WorkflowsLayer struct {
	org               string
	client            forge.Client
	ui                *ui.Printer
	authenticatedUser string
	cliVersion        string
}

// Compile-time check that WorkflowsLayer implements Layer.
var _ Layer = (*WorkflowsLayer)(nil)

// NewWorkflowsLayer creates a new WorkflowsLayer.
// user is the authenticated user who will own CODEOWNERS entries.
// cliVersion is the version of the fullsend CLI performing the install;
// it is injected into the composite action's version input default so
// that workflow runs use the same CLI that produced the scaffold.
func NewWorkflowsLayer(org string, client forge.Client, printer *ui.Printer, user, cliVersion string) *WorkflowsLayer {
	return &WorkflowsLayer{
		org:               org,
		client:            client,
		ui:                printer,
		authenticatedUser: user,
		cliVersion:        cliVersion,
	}
}

func (l *WorkflowsLayer) Name() string {
	return "workflows"
}

// RequiredScopes returns the scopes needed for the given operation.
func (l *WorkflowsLayer) RequiredScopes(op Operation) []string {
	switch op {
	case OpInstall:
		// Writing to .github/workflows/ paths requires the workflow scope.
		// Without it, GitHub returns 404 (not 403), which is deeply confusing.
		return []string{"repo", "workflow"}
	case OpUninstall:
		return nil // no-op
	case OpAnalyze:
		return []string{"repo"}
	default:
		return nil
	}
}

// Install writes the workflow files and CODEOWNERS to the .fullsend repo
// in a single atomic commit using the Git Trees API. If all files already
// match the current tree, no commit is created (idempotent).
func (l *WorkflowsLayer) Install(ctx context.Context) error {
	var files []forge.TreeFile
	err := scaffold.WalkFullsendRepo(func(path string, content []byte) error {
		if path == actionYMLPath {
			content = l.pinVersionInAction(content)
		}
		files = append(files, forge.TreeFile{
			Path:    path,
			Content: content,
			Mode:    scaffold.FileMode(path),
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("collecting scaffold files: %w", err)
	}

	files = append(files, forge.TreeFile{
		Path:    codeownersPath,
		Content: []byte(l.codeownersContent()),
		Mode:    "100644",
	})

	l.ui.StepStart("Writing scaffold files")
	committed, err := l.client.CommitFiles(ctx, l.org, forge.ConfigRepoName,
		"chore: update fullsend scaffold", files)
	if err != nil {
		l.ui.StepFail("Failed to write scaffold files")
		return fmt.Errorf("committing scaffold files: %w", err)
	}
	if committed {
		l.ui.StepDone(fmt.Sprintf("Wrote %d files", len(files)))
	} else {
		l.ui.StepDone("Scaffold up to date")
	}

	return nil
}

// Uninstall is a no-op. Workflow files are removed when the config repo
// is deleted by the ConfigRepoLayer.
func (l *WorkflowsLayer) Uninstall(_ context.Context) error {
	return nil
}

// Analyze checks which managed files exist in the config repo.
func (l *WorkflowsLayer) Analyze(ctx context.Context) (*LayerReport, error) {
	report := &LayerReport{Name: l.Name()}

	var present, missing []string
	for _, path := range managedFiles {
		_, err := l.client.GetFileContent(ctx, l.org, forge.ConfigRepoName, path)
		if err != nil {
			if forge.IsNotFound(err) {
				missing = append(missing, path)
				continue
			}
			return nil, fmt.Errorf("checking %s: %w", path, err)
		}
		present = append(present, path)
	}

	switch {
	case len(missing) == 0:
		report.Status = StatusInstalled
		for _, p := range present {
			report.Details = append(report.Details, p+" exists")
		}
	case len(present) == 0:
		report.Status = StatusNotInstalled
		for _, m := range missing {
			report.WouldInstall = append(report.WouldInstall, "write "+m)
		}
	default:
		report.Status = StatusDegraded
		for _, p := range present {
			report.Details = append(report.Details, p+" exists")
		}
		for _, m := range missing {
			report.WouldFix = append(report.WouldFix, "write "+m)
		}
	}

	return report, nil
}

// pinVersionInAction replaces the "default: latest" line in the action.yml
// version input with the concrete CLI version. If the version is "dev"
// (local build), it falls back to "latest" and logs a warning.
func (l *WorkflowsLayer) pinVersionInAction(content []byte) []byte {
	if l.cliVersion == "" || l.cliVersion == "dev" {
		if l.cliVersion == "dev" {
			l.ui.StepWarn("CLI version is \"dev\"; action.yml will use \"latest\" (unpinned)")
		}
		return content
	}
	pinned := []byte(fmt.Sprintf("    default: %s", l.cliVersion))
	return bytes.Replace(content, versionDefault, pinned, 1)
}

func (l *WorkflowsLayer) codeownersContent() string {
	return fmt.Sprintf("# fullsend configuration is governed by org admins.\n* @%s\n", l.authenticatedUser)
}
