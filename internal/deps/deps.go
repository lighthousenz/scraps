//go:build deps

package deps

import (
	_ "github.com/Masterminds/semver/v3"
	_ "github.com/google/go-cmp/cmp"
	_ "github.com/santhosh-tekuri/jsonschema/v6"
	_ "go.yaml.in/yaml/v3"
)
