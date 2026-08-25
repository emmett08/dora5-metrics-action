package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestActionMetadataArgumentsReferenceDeclaredInputs(t *testing.T) {
	data, err := os.ReadFile("../../action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Name        string         `yaml:"name"`
		Description string         `yaml:"description"`
		Author      string         `yaml:"author"`
		Branding    map[string]any `yaml:"branding"`
		Inputs      map[string]any `yaml:"inputs"`
		Outputs     map[string]any `yaml:"outputs"`
		Runs        struct {
			Using string   `yaml:"using"`
			Image string   `yaml:"image"`
			Args  []string `yaml:"args"`
		} `yaml:"runs"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&action); err != nil {
		t.Fatal(err)
	}
	if action.Runs.Using != "docker" || len(action.Runs.Args) == 0 {
		t.Fatalf("unexpected action runtime: %#v", action.Runs)
	}
	for _, argument := range action.Runs.Args {
		if !strings.HasPrefix(argument, "--") || !strings.Contains(argument, "=${{ inputs.") {
			continue
		}
		name := strings.TrimPrefix(strings.SplitN(argument, "=", 2)[0], "--")
		if _, ok := action.Inputs[name]; !ok {
			t.Errorf("argument %q references undeclared input %q", argument, name)
		}
	}
}
