package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	compiledTemplateTestTask   = "api:build"
	compiledTemplateTestBranch = "dispat-task-run-abc"
)

func TestCompiledTemplateBindsOnlyEffectiveInputs(t *testing.T) {
	template := ChoreographyCompiledTemplate{Task: compiledTemplateTestTask,
		Kind: KindBuild, Weight: 1,
		Package: &AssignmentPackage{Name: "api", Version: "1.0.0", Repository: "source", Dir: "api"},
		Frame:   &AssignmentFrame{Commands: []string{"go build ./..."}},
		Shell:   []string{"sh", "-c"}, Outputs: []string{"dist"}}
	bindings := ChoreographyTaskBindings{Repositories: []AssignmentRepository{{
		Name: "source", Snapshot: "pinned-source", Branch: "signed-source"}},
		PredecessorResultOIDs: []string{"signed-predecessor"}}
	staticDigest, err := DigestChoreographyCompiledTemplate(template)
	require.NoError(t, err)
	header := Header{Protocol: ChoreographyProtocolVersion, Kind: KindBuild,
		Run: "run", Generation: "generation", PlanDigest: "plan", Task: compiledTemplateTestTask,
		Attempt: 1, Node: "worker", Branch: compiledTemplateTestBranch}
	assignment, err := BindChoreographyCompiledTemplate(template, bindings, header, "bootstrap", "graph")
	require.NoError(t, err)
	require.Equal(t, staticDigest, assignment.TemplateDigest)
	require.Equal(t, "signed-predecessor", bindings.PredecessorResultOIDs[0])
	assignment.Frame.Commands[0] = "changed"
	assignment.Repositories[0].Snapshot = "changed-source"
	require.Equal(t, "go build ./...", template.Frame.Commands[0])
	require.Equal(t, "pinned-source", bindings.Repositories[0].Snapshot)

	tampered := template
	tampered.Frame = &AssignmentFrame{Commands: []string{"go test ./..."}}
	tamperedDigest, err := DigestChoreographyCompiledTemplate(tampered)
	require.NoError(t, err)
	require.NotEqual(t, staticDigest, tamperedDigest)
}

func TestCompiledTemplateVerificationUsesSignedJSONIdentity(t *testing.T) {
	template := ChoreographyCompiledTemplate{Task: compiledTemplateTestTask, Kind: KindBuild, Weight: 1}
	digest, err := DigestChoreographyCompiledTemplate(template)
	require.NoError(t, err)
	assignment := Assignment{Header: Header{Kind: KindBuild, Task: compiledTemplateTestTask},
		Weight: 1, TemplateDigest: digest, StaticEnv: []string{}, Shell: []string{}}
	require.NoError(t, VerifyChoreographyAssignmentTemplate(template, assignment))
	assignment.Script = "changed command"
	require.Error(t, VerifyChoreographyAssignmentTemplate(template, assignment))
}
