package view

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// PresentTreeYAML writes TreeOutput as YAML to w.
func PresentTreeYAML(w io.Writer, tree TreeOutput) error {
	yamlBytes, err := yaml.Marshal(tree)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	_, err = w.Write(yamlBytes)
	return err
}

// PresentDetailYAML writes DetailOutput as YAML to w.
func PresentDetailYAML(w io.Writer, detail DetailOutput) error {
	yamlBytes, err := yaml.Marshal(detail)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	_, err = w.Write(yamlBytes)
	return err
}
