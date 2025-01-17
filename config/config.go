package config

import (
	"fmt"
	"io"
	"io/ioutil"
	"sort"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/ghodss/yaml"
)

// Project defines collection of APIs and the standards they adhere to.
type Project struct {
	Version    string                `json:"version"`
	Linters    map[string]*Linter    `json:"linters,omitempty"`
	Generators map[string]*Generator `json:"generators,omitempty"`
	APIs       map[string]*API       `json:"apis"`
}

// Linter describes a set of standards and rules that an API should satisfy.
type Linter struct {
	Name        string             `json:"-"`
	Description string             `json:"description,omitempty"`
	Spectral    *SpectralLinter    `json:"spectral"`
	SweaterComb *SweaterCombLinter `json:"sweater-comb"`
}

// SpectralLinter identifies a Linter as a collection of Spectral rulesets.
type SpectralLinter struct {

	// Rules are a list of Spectral ruleset file locations
	Rules []string `json:"rules"`

	// ExtraArgs may be used to pass extra arguments to `spectral lint`. If not
	// specified, the default arguments `--format text` are used when running
	// spectral. The `-r` flag must not be specified here, as this argument is
	// automatically added from the Rules setting above.
	//
	// See https://meta.stoplight.io/docs/spectral/ZG9jOjI1MTg1-spectral-cli
	// for the options supported.
	ExtraArgs []string `json:"extraArgs"`
}

const defaultSweaterCombImage = "gcr.io/snyk-main/sweater-comb:latest"

// SweaterCombLinter identifies a Sweater Comb Linter, which is distributed as
// a self-contained docker image.
type SweaterCombLinter struct {
	// Image identifies the Sweater Comb docker image to use for linting.
	Image string

	// Rules are a list of Spectral ruleset file locations
	// These may be absolute paths to Sweater Comb rules, such as /rules/apinext.yaml.
	// Or, they may be relative paths to files in this project.
	Rules []string `json:"rules"`

	// ExtraArgs may be used to pass extra arguments to `spectral lint`. The
	// Sweater Comb image includes Spectral. This has the same function as
	// SpectralLinter.ExtraArgs above.
	ExtraArgs []string `json:"extraArgs"`
}

// Generator describes how files are generated for a resource.
type Generator struct {
	Name     string                    `json:"-"`
	Scope    GeneratorScope            `json:"scope"`
	Filename string                    `json:"filename,omitempty"`
	Template string                    `json:"template"`
	Files    string                    `json:"files,omitempty"`
	Data     map[string]*GeneratorData `json:"data,omitempty"`
}

type GeneratorScope string

const (
	GeneratorScopeDefault  = ""
	GeneratorScopeVersion  = "version"
	GeneratorScopeResource = "resource"
)

// GeneratorData describes an item that is added to a generator's template data
// context.
type GeneratorData struct {
	FieldName string `json:"-"`
	Include   string `json:"include"`
}

// An API defines how and where to build versioned OpenAPI documents from a
// source collection of individual resource specifications and additional
// overlay content to merge.
type API struct {
	Name      string         `json:"-"`
	Resources []*ResourceSet `json:"resources"`
	Overlays  []*Overlay     `json:"overlays"`
	Output    *Output        `json:"output"`
}

// A ResourceSet defines a set of versioned resources that adhere to the same
// standards.
//
// Versioned resources are expressed as individual OpenAPI documents in a
// directory structure:
//
// +-resource
//   |
//   +-2021-08-01
//   | |
//   | +-spec.yaml
//   | +-<implementation code, etc. can go here>
//   |
//   +-2021-08-15
//   | |
//   | +-spec.yaml
//   | +-<implementation code, etc. can go here>
//   ...
//
// Each YYYY-mm-dd directory under a resource is a version.  The spec.yaml
// in each version is a complete OpenAPI document describing the resource
// at that version.
type ResourceSet struct {
	Description     string                        `json:"description"`
	Linter          string                        `json:"linter"`
	LinterOverrides map[string]map[string]*Linter `json:"linter-overrides"`
	Generators      []string                      `json:"generators"`
	Path            string                        `json:"path"`
	Excludes        []string                      `json:"excludes"`
}

// An Overlay defines additional OpenAPI documents to merge into the aggregate
// OpenAPI spec when compiling an API. These might include special endpoints
// that should be included in the aggregate API but are not versioned, or
// top-level descriptions of the API itself.
type Overlay struct {
	Include string `json:"include"`
	Inline  string `json:"inline"`
}

// Output defines where the aggregate versioned OpenAPI specs should be created
// during compilation.
type Output struct {
	Path   string `json:"path"`
	Linter string `json:"linter"`
}

// APINames returns the API names in deterministic ascending order.
func (p *Project) APINames() []string {
	var result []string
	for k := range p.APIs {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

func (p *Project) init() {
	if p.Linters == nil {
		p.Linters = map[string]*Linter{}
	}
	for k, v := range p.Linters {
		v.Name = k
	}
	if p.Generators == nil {
		p.Generators = map[string]*Generator{}
	}
	for k, v := range p.Generators {
		v.Name = k
		if v.Scope == GeneratorScopeDefault {
			v.Scope = GeneratorScopeVersion
		}
	}
	if p.APIs == nil {
		p.APIs = map[string]*API{}
	}
	for apiName, api := range p.APIs {
		api.Name = apiName
	}
}

func (p *Project) validate() error {
	if p.Version == "" {
		p.Version = "1"
	}
	if p.Version != "1" {
		return fmt.Errorf("unsupported version %q", p.Version)
	}
	if len(p.APIs) == 0 {
		return fmt.Errorf("no apis defined")
	}
	// Referenced linters and generators all exist
	for _, api := range p.APIs {
		if len(api.Resources) == 0 {
			return fmt.Errorf("no resources defined (apis.%s.resources)", api.Name)
		}
		for rcIndex, resource := range api.Resources {
			if resource.Linter != "" {
				if _, ok := p.Linters[resource.Linter]; !ok {
					return fmt.Errorf("linter %q not found (apis.%s.resources[%d].linter)",
						resource.Linter, api.Name, rcIndex)
				}
			}
			for genIndex, genName := range resource.Generators {
				if _, ok := p.Generators[genName]; !ok {
					return fmt.Errorf("generator %q not found (apis.%s.resources[%d].generator[%d])",
						genName, api.Name, rcIndex, genIndex)
				}
			}
			if err := resource.validate(); err != nil {
				return fmt.Errorf("%w (apis.%s.resources[%d])", err, api.Name, rcIndex)
			}
			for rcName, versionMap := range resource.LinterOverrides {
				for version, linter := range versionMap {
					err := linter.validate()
					if err != nil {
						return fmt.Errorf("%w: (apis.%s.resources[%d].linter-overrides.%s.%s)",
							err, api.Name, rcIndex, rcName, version)
					}
				}
			}
		}
		if api.Output != nil && api.Output.Linter != "" {
			if api.Output.Linter != "" {
				if _, ok := p.Linters[api.Output.Linter]; !ok {
					return fmt.Errorf("linter %q not found (apis.%s.output.linter)",
						api.Output.Linter, api.Name)
				}
			}
		}
	}
	for _, linter := range p.Linters {
		if err := linter.validate(); err != nil {
			return err
		}
		if linter.Spectral != nil && len(linter.Spectral.ExtraArgs) == 0 {
			linter.Spectral.ExtraArgs = defaultSpectralExtraArgs
		}
		if linter.SweaterComb != nil {
			if len(linter.SweaterComb.ExtraArgs) == 0 {
				linter.SweaterComb.ExtraArgs = defaultSpectralExtraArgs
			}
			if linter.SweaterComb.Image == "" {
				linter.SweaterComb.Image = defaultSweaterCombImage
			}
		}
	}
	for _, gen := range p.Generators {
		if err := gen.validate(); err != nil {
			return err
		}
	}
	return nil
}

var defaultSpectralExtraArgs = []string{"--format", "text"}

func (r *ResourceSet) validate() error {
	for _, exclude := range r.Excludes {
		if !doublestar.ValidatePattern(exclude) {
			return fmt.Errorf("invalid exclude pattern %q", exclude)
		}
	}
	return nil
}

func (l *Linter) validate() error {
	// This can be a linter variant dispatch off non-nil if/when more linter
	// types are supported.
	if l.Spectral == nil && l.SweaterComb == nil {
		return fmt.Errorf("missing configuration (linters.%s)", l.Name)
	}
	return nil
}

func (g *Generator) validate() error {
	switch g.Scope {
	case GeneratorScopeVersion:
	//case GeneratorScopeResource:  // TODO: support resource scope
	default:
		return fmt.Errorf("invalid scope %q (generators.%s.scope)", g.Scope, g.Name)
	}
	if g.Template == "" {
		return fmt.Errorf("required field not specified (generators.%s.contents)", g.Name)
	}
	if g.Filename == "" && g.Files == "" {
		return fmt.Errorf("filename or files must be specified (generators.%s)", g.Name)
	}
	for k, v := range g.Data {
		if k == "" {
			return fmt.Errorf("empty key not allowed (generators.%s.data)", g.Name)
		}
		if v.Include == "" {
			return fmt.Errorf("required field not specified (generators.%s.data.%s.include)", g.Name, k)
		}
	}
	return nil
}

// Load loads a Project configuration from its YAML representation.
func Load(r io.Reader) (*Project, error) {
	var p Project
	buf, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read project configuration: %w", err)
	}
	err = yaml.Unmarshal(buf, &p)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal project configuration: %w", err)
	}
	p.init()
	return &p, p.validate()
}

// Save saves a Project configuration to YAML.
func Save(w io.Writer, proj *Project) error {
	buf, err := yaml.Marshal(proj)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}
