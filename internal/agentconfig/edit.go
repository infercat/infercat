package agentconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

func yamlTree(raw []byte) (*yaml.Node, error) {
	var n, extra yaml.Node
	d := yaml.NewDecoder(bytes.NewReader(raw))
	if err := d.Decode(&n); err != nil && err != io.EOF {
		return nil, err
	}
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("expected one YAML document")
	}
	var check func(*yaml.Node) error
	check = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
			return errors.New("YAML aliases/merges are not managed")
		}
		seen := map[string]bool{}
		for i, c := range n.Content {
			if n.Kind == yaml.MappingNode && i%2 == 0 {
				if c.Kind != yaml.ScalarNode || seen[c.Value] {
					return errors.New("ambiguous YAML key")
				}
				seen[c.Value] = true
			}
			if err := check(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(&n); err != nil {
		return nil, err
	}
	return &n, nil
}
func validYAML(raw []byte) error { _, err := yamlTree(raw); return err }

// Only insert at structural line boundaries. Never serialize a person's document.
func dshBlock(raw, provider []byte) ([]byte, int, error) {
	n, err := yamlTree(raw)
	if err != nil {
		return nil, 0, err
	}
	nl := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		nl = "\r\n"
	}
	pos, indent, headers := len(raw), "", ""
	var parent *yaml.Node
	if len(n.Content) > 0 {
		parent = n.Content[0]
	}
	for _, key := range []string{"llm-pi-ai", "providers", "infercat"} {
		if parent == nil {
			if key != "infercat" {
				headers += indent + key + ":" + nl
				indent += "  "
			}
			continue
		}
		if parent.Kind != yaml.MappingNode || parent.Style&yaml.FlowStyle != 0 {
			return nil, 0, errors.New("expected YAML block maps; custom/flow shapes require manual configuration")
		}
		var found *yaml.Node
		for i := 0; i < len(parent.Content); i += 2 {
			if parent.Content[i].Value == key {
				found = parent.Content[i+1]
			}
		}
		if key == "infercat" && found != nil {
			return nil, 0, errors.New("provider infercat already exists without our receipt")
		}
		if found == nil {
			if len(parent.Content) > 0 {
				first := parent.Content[0]
				pos = 0
				for line := 1; line < first.Line; line++ {
					j := bytes.IndexByte(raw[pos:], '\n')
					if j < 0 {
						return nil, 0, errors.New("invalid YAML position")
					}
					pos += j + 1
				}
				indent = strings.Repeat(" ", first.Column-1)
			}
			if key != "infercat" {
				headers += indent + key + ":" + nl
				indent += "  "
			}
			parent = nil
		} else {
			if found.Kind == yaml.ScalarNode && found.Tag == "!!null" && found.Value == "" {
				pos = 0
				for line := 1; line <= found.Line; line++ {
					j := bytes.IndexByte(raw[pos:], '\n')
					if j < 0 {
						pos = len(raw)
						break
					}
					pos += j + 1
				}
				indent += "  "
				parent = nil
			} else {
				parent = found
				indent += "  "
			}
		}
	}
	prefix := ""
	if pos > 0 && raw[pos-1] != '\n' {
		prefix = nl
	}
	block := prefix + "# BEGIN infercat managed provider" + nl + headers + indent + "infercat: " + string(provider) + nl + "# END infercat managed provider" + nl
	result := append(append(append([]byte{}, raw[:pos]...), block...), raw[pos:]...)
	if err := validYAML(result); err != nil {
		return nil, 0, fmt.Errorf("cannot safely insert provider: %w", err)
	}
	return []byte(block), pos, nil
}

func removalSafe(raw, block []byte) error {
	if err := validYAML(raw); err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	text := strings.ReplaceAll(string(block), "\r\n", "\n")
	llm, _ := doc["llm-pi-ai"].(map[string]any)
	providers, _ := llm["providers"].(map[string]any)
	if strings.Contains(text, "\nllm-pi-ai:\n") && len(llm) != 1 || strings.Contains(text, "providers:\n") && len(providers) != 1 {
		return errors.New("foreign entries depend on managed parent; file preserved")
	}
	return validYAML(bytes.Replace(raw, block, nil, 1))
}
