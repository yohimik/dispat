package release

import (
	"encoding/json"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

func appendWorkspaceOwners(p *plan.Plan, env []string) []string {
	owners := make(map[string]string)
	conflicts := make(map[string]bool)
	for name, rel := range p.Releases {
		if rel.Pkg.Repository == "" {
			continue
		}
		key := plan.PackageCommitExportPrefix + plan.EnvKey(name)
		if owner, exists := owners[key]; exists && owner != rel.Pkg.Repository {
			conflicts[key] = true
		}
		owners[key] = rel.Pkg.Repository
	}
	for key := range conflicts {
		delete(owners, key)
	}
	if len(owners) == 0 {
		return env
	}
	encoded, _ := json.Marshal(owners)
	return append(env, workspaceenv.Owners+"="+string(encoded))
}
