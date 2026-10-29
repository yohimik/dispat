package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func manualNodeCommand(settings options, node nodeFixture, item task, environment []string, output io.Writer) command {
	request := command{Dir: node.Root, Name: settings.Tool, Env: peerEnvironment(node, environment), Output: output}
	request.Env = append(request.Env, "NX_AGENT_NAME="+node.Name)
	request.Env = append(request.Env, "NX_NO_CLOUD=true", "NX_SKIP_NX_CACHE=true", "NX_CLOUD_DISTRIBUTED_EXECUTION=false")
	if settings.Tool == nxTool {
		request.Args = []string{"run", item.Name + ":" + buildScriptName, "--parallel=1", "--skip-nx-cache", "--output-style=stream"}
	} else {
		request.Args = []string{"run", buildScriptName, "--scope", item.Name, "--concurrency=1", "--stream"}
	}
	return request
}

func collectManualArtifacts(ctx context.Context, settings options, data fixture, output io.Writer) error {
	if settings.Remote == nil {
		return fmt.Errorf("manual GCP split has no remote executor plan")
	}
	for index, node := range data.Nodes {
		destination := filepath.Join(settings.Output, "manual-results", node.Name)
		if err := os.MkdirAll(destination, directoryMode); err != nil {
			return err
		}
		if _, err := callRemote(ctx, settings.Remote, remoteRequest{Action: remoteFetchAction, Node: node.Name, TrialID: data.Run, Destination: destination}, output); err != nil {
			return err
		}
		item := data.Tasks[index]
		from, to := filepath.Join(destination, artifactsDirectory, item.Name), filepath.Join(data.Root, artifactsDirectory, item.Name)
		if err := os.MkdirAll(to, directoryMode); err != nil {
			return err
		}
		for _, name := range workloadOutputs(item.Workload, item.Profile) {
			if _, err := copyArtifact(filepath.Join(from, name), filepath.Join(to, name)); err != nil {
				return err
			}
		}
		content, err := os.ReadFile(filepath.Join(from, manifestFileName))
		if err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(to, manifestFileName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if err != nil {
			return err
		}
		_, err = file.Write(content)
		closeError := file.Close()
		if err != nil {
			return err
		}
		if closeError != nil {
			return closeError
		}
	}
	return nil
}
