package privileged

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	DefaultManagerConfigPath      = "/etc/lightningos/config.yaml"
	DefaultLNDAdminMacaroonPath   = "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"
	DefaultLNDManagerMacaroonPath = "/var/lib/lightningos-credentials/lnd/manager.macaroon"
	maxManagerConfigBytes         = 1024 * 1024
)

type AtomicConfigFiles struct {
	path string
}

func NewAtomicConfigFiles(path string) *AtomicConfigFiles {
	return &AtomicConfigFiles{path: path}
}

func prepareEnableLoginConfig(data []byte) ([]byte, bool, error) {
	return prepareManagerConfig(data, func(root *yaml.Node) (bool, error) {
		features, found := yamlMapValue(root, "features")
		if !found {
			features = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			root.Content = append(root.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "features"},
				features,
			)
		} else if features.Kind != yaml.MappingNode {
			return false, errors.New("manager config features must be a mapping")
		}

		enableLogin, found := yamlMapValue(features, "enable_login")
		if found && enableLogin.Kind == yaml.ScalarNode && enableLogin.Tag == "!!bool" && enableLogin.Value == "true" {
			return false, nil
		}
		if !found {
			enableLogin = &yaml.Node{}
			features.Content = append(features.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enable_login"},
				enableLogin,
			)
		}
		setYAMLScalar(enableLogin, "!!bool", "true")
		return true, nil
	})
}

func prepareLNDMacaroonPathConfig(data []byte, macaroonPath string) ([]byte, bool, error) {
	if macaroonPath != DefaultLNDAdminMacaroonPath && macaroonPath != DefaultLNDManagerMacaroonPath {
		return nil, false, errors.New("LND macaroon path is not allowed")
	}
	return prepareManagerConfig(data, func(root *yaml.Node) (bool, error) {
		lnd, found := yamlMapValue(root, "lnd")
		if !found {
			return false, errors.New("manager config lnd section is required")
		}
		if lnd.Kind != yaml.MappingNode {
			return false, errors.New("manager config lnd must be a mapping")
		}
		value, found := yamlMapValue(lnd, "admin_macaroon_path")
		if !found {
			value = &yaml.Node{}
			lnd.Content = append(lnd.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "admin_macaroon_path"},
				value,
			)
		}
		if value.Kind == yaml.ScalarNode && value.Tag == "!!str" && value.Value == macaroonPath {
			return false, nil
		}
		setYAMLScalar(value, "!!str", macaroonPath)
		return true, nil
	})
}

func configuredLNDMacaroonPath(data []byte) (string, error) {
	if len(data) == 0 || len(data) > maxManagerConfigBytes {
		return "", errors.New("manager config size is invalid")
	}
	var validation map[string]any
	if err := yaml.Unmarshal(data, &validation); err != nil {
		return "", fmt.Errorf("invalid manager config: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return "", errors.New("manager config root must be a mapping")
	}
	lnd, found := yamlMapValue(document.Content[0], "lnd")
	if !found || lnd.Kind != yaml.MappingNode {
		return "", errors.New("manager config lnd section is required")
	}
	value, found := yamlMapValue(lnd, "admin_macaroon_path")
	if !found || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return "", errors.New("manager config LND macaroon path is required")
	}
	path := value.Value
	if path != DefaultLNDAdminMacaroonPath && path != DefaultLNDManagerMacaroonPath {
		return "", errors.New("manager config LND macaroon path is unsupported")
	}
	return path, nil
}

func prepareManagerConfig(data []byte, mutate func(*yaml.Node) (bool, error)) ([]byte, bool, error) {
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

	changed, err := mutate(document.Content[0])
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return append([]byte(nil), data...), false, nil
	}

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

func setYAMLScalar(node *yaml.Node, tag, value string) {
	node.Kind = yaml.ScalarNode
	node.Tag = tag
	node.Value = value
	node.Style = 0
	node.Content = nil
	node.Anchor = ""
	node.Alias = nil
}

func yamlMapValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}
