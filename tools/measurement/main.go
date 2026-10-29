package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/yohimik/dispat/tools/internal/executionmeasurement"
)

const (
	inputFlag           = "input"
	outputFlag          = "output"
	inputHelp           = "raw measurement JSON"
	outputHelp          = "new derived measurement JSON"
	missingPathsMessage = "measurement input and a fresh output path are required"
	outputFileMode      = 0o600
)

func main() {
	input := flag.String(inputFlag, "", inputHelp)
	output := flag.String(outputFlag, "", outputHelp)
	flag.Parse()
	if err := deriveFile(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func deriveFile(input, output string) error {
	if input == "" || output == "" || input == output {
		return errors.New(missingPathsMessage)
	}
	body, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var measurement executionmeasurement.Measurement
	if err := json.Unmarshal(body, &measurement); err != nil {
		return err
	}
	if err := executionmeasurement.Derive(&measurement); err != nil {
		return err
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(body, &original); err != nil {
		return err
	}
	var graph map[string]json.RawMessage
	if err := json.Unmarshal(original["graph"], &graph); err != nil {
		return err
	}
	graph["counts"], err = json.Marshal(measurement.Graph.Counts)
	if err != nil {
		return err
	}
	original["graph"], err = json.Marshal(graph)
	if err != nil {
		return err
	}
	original["analysis"], err = json.Marshal(measurement.Analysis)
	if err != nil {
		return err
	}
	derived, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		return err
	}
	if err := executionmeasurement.Validate(derived); err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, outputFileMode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(derived, '\n'))
	return errors.Join(writeErr, file.Close())
}
