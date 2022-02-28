package generator

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/ghodss/yaml"

	"github.com/snyk/vervet/v3"
	"github.com/snyk/vervet/v3/config"
)

// Generator generates files for new resources from data models and templates.
type Generator struct {
	name     string
	filename *template.Template
	contents *template.Template
	files    *template.Template
	scope    config.GeneratorScope

	debug bool
	force bool
}

var (
	templateFuncs = template.FuncMap{
		"map": func(keyValues ...interface{}) (map[string]interface{}, error) {
			if len(keyValues)%2 != 0 {
				return nil, fmt.Errorf("invalid number of arguments to map")
			}
			m := make(map[string]interface{}, len(keyValues)/2)
			for i := 0; i < len(keyValues); i += 2 {
				k, ok := keyValues[i].(string)
				if !ok {
					return nil, fmt.Errorf("map keys must be strings")
				}
				m[k] = keyValues[i+1]
			}
			return m, nil
		},
		"indent": func(indent int, s string) string {
			return strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", indent))
		},
		"uncapitalize": func(s string) string {
			if len(s) > 1 {
				return strings.ToLower(s[0:1]) + s[1:]
			}
			return s
		},
		"capitalize": func(s string) string {
			if len(s) > 1 {
				return strings.ToUpper(s[0:1]) + s[1:]
			}
			return s
		},
		"replaceall": strings.ReplaceAll,
		"operations": func(p *openapi3.PathItem) map[string]*openapi3.Operation {
			result := map[string]*openapi3.Operation{}
			if p.Connect != nil {
				result["connect"] = p.Connect
			}
			if p.Delete != nil {
				result["delete"] = p.Delete
			}
			if p.Get != nil {
				result["get"] = p.Get
			}
			if p.Head != nil {
				result["head"] = p.Head
			}
			if p.Options != nil {
				result["options"] = p.Options
			}
			if p.Patch != nil {
				result["patch"] = p.Patch
			}
			if p.Post != nil {
				result["post"] = p.Post
			}
			if p.Put != nil {
				result["put"] = p.Put
			}
			if p.Trace != nil {
				result["trace"] = p.Trace
			}
			return result
		},
	}
)

func withIncludeFunc(t *template.Template) *template.Template {
	return t.Funcs(template.FuncMap{
		"include": func(name string, data interface{}) (string, error) {
			buf := bytes.NewBuffer(nil)
			if err := t.ExecuteTemplate(buf, name, data); err != nil {
				return "", err
			}
			return buf.String(), nil
		},
	})
}

// NewMap instanstiates a map of Generators from configuration.
func NewMap(generatorsConf config.Generators, options ...Option) (map[string]*Generator, error) {
	result := map[string]*Generator{}
	for name, genConf := range generatorsConf {
		g, err := New(genConf, options...)
		if err != nil {
			return nil, err
		}
		result[name] = g
	}
	return result, nil
}

// New returns a new Generator from configuration.
func New(conf *config.Generator, options ...Option) (*Generator, error) {
	g := &Generator{
		name:  conf.Name,
		scope: conf.Scope,
	}
	for i := range options {
		options[i](g)
	}
	if g.debug {
		log.Printf("generator %s: debug logging enabled", g.name)
	}

	contentsTemplate, err := ioutil.ReadFile(conf.Template)
	if err != nil {
		return nil, fmt.Errorf("%w: (generators.%s.contents)", err, conf.Name)
	}
	g.contents, err = template.New("contents").Funcs(templateFuncs).Parse(string(contentsTemplate))
	if err != nil {
		return nil, fmt.Errorf("%w: (generators.%s.contents)", err, conf.Name)
	}
	if conf.Filename != "" {
		g.filename, err = template.New("filename").Funcs(templateFuncs).Parse(conf.Filename)
		if err != nil {
			return nil, fmt.Errorf("%w: (generators.%s.filename)", err, conf.Name)
		}
	}
	if conf.Files != "" {
		g.files, err = withIncludeFunc(g.contents.New("files")).Parse(conf.Files)
		if err != nil {
			return nil, fmt.Errorf("%w: (generators.%s.files)", err, conf.Name)
		}
	}
	return g, nil
}

// Option configures a Generator.
type Option func(g *Generator)

// Force configures the Generator to overwrite generated artifacts.
func Force(force bool) Option {
	return func(g *Generator) {
		g.force = true
	}
}

// Debug turns on template debug logging.
func Debug(debug bool) Option {
	return func(g *Generator) {
		g.debug = true
	}
}

// Execute runs the generator on the given resources.
func (g *Generator) Execute(resources ResourceMap) error {
	switch g.Scope() {
	case config.GeneratorScopeDefault, config.GeneratorScopeVersion:
		for rcKey, rcVersions := range resources {
			for _, version := range rcVersions.Versions() {
				rc, err := rcVersions.At(version.String())
				if err != nil {
					return err
				}
				scope := &VersionScope{
					API:      rcKey.API,
					Path:     filepath.Join(rcKey.Path, version.DateString()),
					Resource: rc,
				}
				err = g.execute(scope)
				if err != nil {
					return err
				}
			}
		}
	case config.GeneratorScopeResource:
		for rcKey, rcVersions := range resources {
			scope := &ResourceScope{
				API:              rcKey.API,
				Path:             rcKey.Path,
				ResourceVersions: rcVersions,
			}
			err := g.execute(scope)
			if err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported generator scope %q", g.Scope())
	}
	return nil
}

// ResourceScope identifies a resource that the generator is building for.
type ResourceScope struct {
	*vervet.ResourceVersions
	API  string
	Path string
}

// VersionScope identifies a distinct version of a resource that the generator
// is building for.
type VersionScope struct {
	*vervet.Resource
	API  string
	Path string
}

// Scope returns the configured scope type of the generator.
func (g *Generator) Scope() config.GeneratorScope {
	return g.scope
}

// execute the Generator. If generated artifacts already exist, a warning
// is logged but the file is not overwritten, unless force is true.
//
// TODO: in Go 1.18, declare scope as an interface{ VersionScope | ResourceScope }
func (g *Generator) execute(scope interface{}) error {
	if g.files != nil {
		return g.runFiles(scope)
	}
	return g.runFile(scope)
}

func (g *Generator) runFile(scope interface{}) error {
	var filenameBuf bytes.Buffer
	err := g.filename.ExecuteTemplate(&filenameBuf, "filename", scope)
	if err != nil {
		return fmt.Errorf("failed to resolve filename: %w (generators.%s.filename)", err, g.name)
	}
	filename := filenameBuf.String()
	if g.debug {
		log.Printf("interpolated generators.%s.filename => %q", g.name, filename)
	}
	if _, err := os.Stat(filename); err == nil && !g.force {
		log.Printf("not overwriting existing file %q", filename)
		return nil
	}
	parentDir := filepath.Dir(filename)
	err = os.MkdirAll(parentDir, 0777)
	if err != nil {
		return fmt.Errorf("failed to create %q: %w: (generators.%s.filename)", parentDir, err, g.name)
	}
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create %q: %w: (generators.%s.filename)", filename, err, g.name)
	}
	defer f.Close()
	err = g.contents.ExecuteTemplate(f, "contents", scope)
	if err != nil {
		return fmt.Errorf("template failed: %w (generators.%s.filename)", err, g.name)
	}
	return nil
}

func (g *Generator) runFiles(scope interface{}) error {
	var filesBuf bytes.Buffer
	err := g.files.ExecuteTemplate(&filesBuf, "files", scope)
	if err != nil {
		return fmt.Errorf("%w: (generators.%s.files)", err, g.name)
	}
	if g.debug {
		log.Printf("interpolated generators.%s.files => %q", g.name, filesBuf.String())
	}
	files := map[string]string{}
	err = yaml.Unmarshal(filesBuf.Bytes(), &files)
	if err != nil {
		// TODO: dump output for debugging?
		return fmt.Errorf("failed to load output as yaml: %w: (generators.%s.files)", err, g.name)
	}
	for filename, contents := range files {
		dir := filepath.Dir(filename)
		err := os.MkdirAll(dir, 0777)
		if err != nil {
			return fmt.Errorf("failed to create directory %q: %w (generators.%s.files)", dir, err, g.name)
		}
		if _, err := os.Stat(filename); err == nil && !g.force {
			log.Printf("not overwriting existing file %q", filename)
			continue
		}
		err = ioutil.WriteFile(filename, []byte(contents), 0777)
		if err != nil {
			return fmt.Errorf("failed to write file %q: %w (generators.%s.files)", filename, err, g.name)
		}
	}
	return nil
}
