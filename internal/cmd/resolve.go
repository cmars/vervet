package cmd

import (
	"fmt"

	"github.com/urfave/cli/v2"

	"github.com/snyk/vervet/v3"
)

// Resolve aggregates, renders and validates resource specs at a particular
// version.
func Resolve(ctx *cli.Context) error {
	specDir, err := absPath(ctx.Args().Get(0))
	if err != nil {
		return err
	}
	specVersions, err := vervet.LoadSpecVersions(specDir)
	if err != nil {
		return err
	}
	version, err := vervet.ParseVersion(ctx.String("at"))
	if err != nil {
		return err
	}
	specVersion, err := specVersions.At(*version)
	if err != nil {
		return err
	}

	yamlBuf, err := vervet.ToSpecYAML(specVersion)
	if err != nil {
		return fmt.Errorf("failed to convert JSON to YAML: %w", err)
	}
	fmt.Println(string(yamlBuf))

	err = specVersion.Validate(ctx.Context)
	if err != nil {
		return fmt.Errorf("error: spec validation failed: %w", err)
	}
	return nil
}