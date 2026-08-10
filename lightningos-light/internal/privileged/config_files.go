package privileged

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	DefaultManagerConfigPath = "/etc/lightningos/config.yaml"
	maxManagerConfigBytes    = 1024 * 1024
)

type AtomicConfigFiles struct {
	path string
}

func NewAtomicConfigFiles(path string) *AtomicConfigFiles {
	return &AtomicConfigFiles{path: path}
}

func prepareEnableLoginConfig(data []byte) ([]byte, bool, error) {
	if len(data) == 0 {
		return nil, false, errors.New("manager config is empty")
	}
	if len(data) > maxManagerConfigBytes {
		return nil, false, errors.New("manager config is too large")
	}

	// Decoding into a map first makes yaml.v3 reject duplicate mapping keys.
	var validation map[string]any
	if err := yaml.Unmarshal(data, &validation); err != nil {
		return nil, false, fmt.Errorf("invalid manager config: %w", err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, false, fmt.Errorf("invalid manager config: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, false, errors.New("manager config root must be a mapping")
	}

	root := document.Content[0]
	features, found := yamlMapValue(root, "features")
	if !found {
		features = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "features"},
			features,
		)
	} else if features.Kind != yaml.MappingNode {
		return nil, false, errors.New("manager config features must be a mapping")
	}

	enableLogin, found := yamlMapValue(features, "enable_login")
	if found && enableLogin.Kind == yaml.ScalarNode && enableLogin.Tag == "!!bool" && enableLogin.Value == "true" {
		return append([]byte(nil), data...), false, nil
	}
	if !found {
		enableLogin = &yaml.Node{}
		features.Content = append(features.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enable_login"},
			enableLogin,
		)
	}
	enableLogin.Kind = yaml.ScalarNode
	enableLogin.Tag = "!!bool"
	enableLogin.Value = "true"
	enableLogin.Style = 0
	enableLogin.Content = nil
	enableLogin.Anchor = ""
	enableLogin.Alias = nil

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, false, fmt.Errorf("encode manager config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("encode manager config: %w", err)
	}
	if output.Len() > maxManagerConfigBytes {
		return nil, false, errors.New("updated manager config is too large")
	}
	validation = nil
	if err := yaml.Unmarshal(output.Bytes(), &validation); err != nil {
		return nil, false, fmt.Errorf("validate updated manager config: %w", err)
	}
	return output.Bytes(), true, nil
}

func yamlMapValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}
