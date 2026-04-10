package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"tclaw/internal/user"

	"gopkg.in/yaml.v3"
)

// Writer provides atomic YAML mutations to the config file. All writes are
// serialized by a mutex and use a temp-file-plus-rename pattern to ensure
// the config file is never left in a partial state.
type Writer struct {
	path string
	env  Env
	mu   sync.Mutex
}

// NewWriter creates a Writer for the config file at path, targeting the given
// environment section.
func NewWriter(path string, env Env) *Writer {
	return &Writer{path: path, env: env}
}

// AddChannel appends a channel to a user's channel list in the YAML file.
// Returns an error if a channel with the same name already exists.
func (w *Writer) AddChannel(userID user.ID, ch Channel) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.mutate(func(channels *yaml.Node) error {
		// Reject duplicates at the config level — even if the in-memory registry
		// check passes, the on-disk config is the source of truth.
		for _, existing := range channels.Content {
			if nodeScalarValue(existing, "name") == ch.Name {
				return fmt.Errorf("channel %q already exists in config", ch.Name)
			}
		}

		node, err := marshalChannelToNode(ch)
		if err != nil {
			return fmt.Errorf("marshal channel: %w", err)
		}
		channels.Content = append(channels.Content, node)
		return nil
	}, userID)
}

// UpdateChannel modifies a channel by name using the provided function.
func (w *Writer) UpdateChannel(userID user.ID, name string, fn func(*Channel)) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.mutate(func(channels *yaml.Node) error {
		for i, node := range channels.Content {
			nodeName := nodeScalarValue(node, "name")
			if nodeName != name {
				continue
			}

			// Decode the node into a Channel, apply the mutation, re-encode.
			var ch Channel
			if err := node.Decode(&ch); err != nil {
				return fmt.Errorf("decode channel %q: %w", name, err)
			}
			fn(&ch)
			replacement, err := marshalChannelToNode(ch)
			if err != nil {
				return fmt.Errorf("marshal updated channel %q: %w", name, err)
			}
			channels.Content[i] = replacement
			return nil
		}
		return fmt.Errorf("channel %q not found", name)
	}, userID)
}

// RemoveChannel removes a channel by name from the user's channel list.
func (w *Writer) RemoveChannel(userID user.ID, name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.mutate(func(channels *yaml.Node) error {
		for i, node := range channels.Content {
			nodeName := nodeScalarValue(node, "name")
			if nodeName != name {
				continue
			}
			channels.Content = append(channels.Content[:i], channels.Content[i+1:]...)
			return nil
		}
		return fmt.Errorf("channel %q not found", name)
	}, userID)
}

// ReadChannels returns all channels for a user by reading the YAML file directly.
// Unlike ReloadConfig, this does not resolve secrets — it returns raw config values.
func (w *Writer) ReadChannels(userID user.ID) ([]Channel, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, channels, err := w.findChannelsNode(userID)
	if err != nil {
		return nil, err
	}

	var result []Channel
	for _, node := range channels.Content {
		var ch Channel
		if err := node.Decode(&ch); err != nil {
			return nil, fmt.Errorf("decode channel: %w", err)
		}
		result = append(result, ch)
	}
	return result, nil
}

// ReloadConfig re-reads and parses the config file with full secret resolution.
func (w *Writer) ReloadConfig() (*Config, error) {
	return Load(w.path, w.env)
}

// mutate reads the YAML file, navigates to the channels sequence for the given
// user, calls fn to modify it, and writes the result atomically.
func (w *Writer) mutate(fn func(channels *yaml.Node) error, userID user.ID) error {
	root, channels, err := w.findChannelsNode(userID)
	if err != nil {
		return err
	}

	if err := fn(channels); err != nil {
		return err
	}

	return w.writeAtomic(root)
}

// findChannelsNode reads the YAML file and navigates to the channels sequence
// node for the given user. Returns the root document node (for writing back)
// and the channels sequence node.
func (w *Writer) findChannelsNode(userID user.ID) (*yaml.Node, *yaml.Node, error) {
	data, err := os.ReadFile(w.path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}

	// The document node wraps the root mapping.
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil, fmt.Errorf("unexpected YAML structure: expected document node")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("unexpected YAML structure: expected root mapping")
	}

	// Find the environment key in the root mapping.
	envNode := findMappingValue(root, string(w.env))
	if envNode == nil {
		return nil, nil, fmt.Errorf("environment %q not found in config", w.env)
	}

	// Find the users sequence.
	usersNode := findMappingValue(envNode, "users")
	if usersNode == nil {
		return nil, nil, fmt.Errorf("no users key in environment %q", w.env)
	}
	if usersNode.Kind != yaml.SequenceNode {
		return nil, nil, fmt.Errorf("users is not a sequence")
	}

	// Find the user by ID.
	for _, userNode := range usersNode.Content {
		if userNode.Kind != yaml.MappingNode {
			continue
		}
		idVal := findMappingValue(userNode, "id")
		if idVal == nil || idVal.Value != string(userID) {
			continue
		}

		// Find the channels sequence.
		channelsNode := findMappingValue(userNode, "channels")
		if channelsNode == nil {
			// No channels key yet — create one.
			channelsNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			userNode.Content = append(userNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "channels"},
				channelsNode,
			)
		}
		if channelsNode.Kind != yaml.SequenceNode {
			return nil, nil, fmt.Errorf("channels for user %q is not a sequence", userID)
		}

		return &doc, channelsNode, nil
	}

	return nil, nil, fmt.Errorf("user %q not found in config", userID)
}

// writeAtomic writes the YAML document to a temp file and renames it into place.
func (w *Writer) writeAtomic(doc *yaml.Node) error {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(w.path)
	tmp, err := os.CreateTemp(dir, "tclaw-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, w.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}

	return nil
}

// findMappingValue returns the value node for a given key in a mapping node.
func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// nodeScalarValue returns the string value of a scalar key in a mapping node.
func nodeScalarValue(mapping *yaml.Node, key string) string {
	v := findMappingValue(mapping, key)
	if v == nil {
		return ""
	}
	return v.Value
}

// marshalChannelToNode marshals a Channel struct into a yaml.Node suitable
// for insertion into a sequence.
func marshalChannelToNode(ch Channel) (*yaml.Node, error) {
	data, err := yaml.Marshal(ch)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	// Unmarshal wraps in a document node — return the inner mapping.
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0], nil
	}
	return &node, nil
}
