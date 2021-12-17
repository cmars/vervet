package optic

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"

	"github.com/snyk/vervet/config"
	"github.com/snyk/vervet/internal/files"
	"github.com/snyk/vervet/testdata"
)

func TestNewLocalFile(t *testing.T) {
	c := qt.New(t)
	ctx, cancel := context.WithCancel(context.TODO())
	c.Cleanup(cancel)

	// Sanity check constructor
	l, err := New(ctx, &config.OpticCILinter{
		Image:    "some-image",
		Original: "",
		Proposed: "",
	})
	c.Assert(err, qt.IsNil)
	c.Assert(l.image, qt.Equals, "some-image")
	c.Assert(l.fromSource, qt.DeepEquals, files.NilSource{})
	c.Assert(l.toSource, qt.DeepEquals, files.LocalFSSource{})

	// Set up a local example project
	testProject := c.TempDir()
	versionDir := testProject + "/hello/2021-06-01"
	c.Assert(os.MkdirAll(versionDir, 0777), qt.IsNil)
	copyFile(c, filepath.Join(versionDir, "spec.yaml"), testdata.Path("resources/_examples/hello-world/2021-06-01/spec.yaml"))
	origWd, err := os.Getwd()
	c.Assert(err, qt.IsNil)
	c.Cleanup(func() { c.Assert(os.Chdir(origWd), qt.IsNil) })
	c.Assert(os.Chdir(testProject), qt.IsNil)

	// Mock time for repeatable tests
	l.timeNow = func() time.Time { return time.Date(2021, time.October, 30, 1, 2, 3, 0, time.UTC) }

	// Capture stdout to a file
	tempFile, err := os.Create(c.TempDir() + "/stdout")
	c.Assert(err, qt.IsNil)
	c.Patch(&os.Stdout, tempFile)
	defer tempFile.Close()

	runner := &mockRunner{}
	l.runner = runner
	err = l.Run(ctx, "hello", "hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.IsNil)
	c.Assert(runner.runs, qt.HasLen, 1)
	c.Assert(strings.Join(runner.runs[0], " "), qt.Matches,
		``+
			`^docker run --rm -v .*:/input.json -v .*/hello:/to/hello `+
			`some-image bulk-compare --input /input.json`)

	// Verify captured output was substituted. Mainly a convenience that makes
	// output host-relevant and cmd-clickable if possible.
	c.Assert(tempFile.Sync(), qt.IsNil)
	capturedOutput, err := ioutil.ReadFile(tempFile.Name())
	c.Assert(err, qt.IsNil)
	c.Assert(string(capturedOutput), qt.Equals, "(does not exist):here.yaml (local file):eternity.yaml\n")

	// Command failed.
	runner = &mockRunner{err: fmt.Errorf("bad wolf")}
	l.runner = runner
	err = l.Run(ctx, "hello", "hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.ErrorMatches, ".*: bad wolf")
}

func TestNoSuchWorkingCopyFile(t *testing.T) {
	c := qt.New(t)
	path, err := files.LocalFSSource{}.Fetch(uuid.New().String())
	c.Assert(err, qt.IsNil)
	c.Assert(path, qt.Equals, "")
}

func TestNoSuchGitFile(t *testing.T) {
	c := qt.New(t)
	testRepo, commitHash := setupGitRepo(c)
	gitSource, err := newGitRepoSource(testRepo, commitHash.String())
	c.Assert(err, qt.IsNil)
	c.Cleanup(func() { c.Assert(gitSource.Close(), qt.IsNil) })
	_, err = gitSource.Prefetch("hello")
	c.Assert(err, qt.IsNil)
	path, err := gitSource.Fetch("hello/" + uuid.New().String())
	c.Assert(err, qt.IsNil)
	c.Assert(path, qt.Equals, "")
}

func TestNoSuchGitBranch(t *testing.T) {
	c := qt.New(t)
	testRepo, _ := setupGitRepo(c)
	_, err := newGitRepoSource(testRepo, "nope")
	c.Assert(err, qt.ErrorMatches, "reference not found")
}

func TestNewGitFile(t *testing.T) {
	c := qt.New(t)
	ctx, cancel := context.WithCancel(context.TODO())
	c.Cleanup(cancel)

	testRepo, commitHash := setupGitRepo(c)
	origWd, err := os.Getwd()
	c.Assert(err, qt.IsNil)
	c.Cleanup(func() { c.Assert(os.Chdir(origWd), qt.IsNil) })
	c.Assert(os.Chdir(testRepo), qt.IsNil)

	// Sanity check constructor
	l, err := New(ctx, &config.OpticCILinter{
		Image:    "some-image",
		Original: commitHash.String(),
		Proposed: "",
	})
	c.Assert(err, qt.IsNil)
	c.Assert(l.image, qt.Equals, "some-image")
	c.Assert(l.fromSource, qt.Satisfies, func(v interface{}) bool {
		_, ok := v.(*gitRepoSource)
		return ok
	})
	c.Assert(l.toSource, qt.DeepEquals, files.LocalFSSource{})

	// Sanity check gitRepoSource
	_, err = l.fromSource.Prefetch("hello")
	c.Assert(err, qt.IsNil)
	path, err := l.fromSource.Fetch("hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.IsNil)
	c.Assert(path, qt.Not(qt.Equals), "")

	runner := &mockRunner{}
	l.runner = runner
	err = l.Run(ctx, "hello", "hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.IsNil)
	c.Assert(runner.runs, qt.HasLen, 1)
	c.Assert(strings.Join(runner.runs[0], " "), qt.Matches,
		``+
			`^docker run --rm -v .*:/input.json -v .*/hello:/to/hello `+
			`some-image bulk-compare --input /input.json`)

	// Command failed.
	runner = &mockRunner{err: fmt.Errorf("bad wolf")}
	l.runner = runner
	err = l.Run(ctx, "hello", "hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.ErrorMatches, ".*: bad wolf")
}

func TestGitScript(t *testing.T) {
	c := qt.New(t)
	ctx, cancel := context.WithCancel(context.TODO())
	c.Cleanup(cancel)

	testRepo, commitHash := setupGitRepo(c)
	origWd, err := os.Getwd()
	c.Assert(err, qt.IsNil)
	c.Cleanup(func() { c.Assert(os.Chdir(origWd), qt.IsNil) })
	c.Assert(os.Chdir(testRepo), qt.IsNil)

	// Sanity check constructor
	l, err := New(ctx, &config.OpticCILinter{
		Script:   "/usr/local/lib/node_modules/.bin/sweater-comb",
		Original: commitHash.String(),
		Proposed: "",
	})
	c.Assert(err, qt.IsNil)
	c.Assert(l.image, qt.Equals, "")
	c.Assert(l.script, qt.Equals, "/usr/local/lib/node_modules/.bin/sweater-comb")
	c.Assert(l.fromSource, qt.Satisfies, func(v interface{}) bool {
		_, ok := v.(*gitRepoSource)
		return ok
	})
	c.Assert(l.toSource, qt.DeepEquals, files.LocalFSSource{})

	// Sanity check gitRepoSource
	_, err = l.fromSource.Prefetch("hello")
	c.Assert(err, qt.IsNil)
	path, err := l.fromSource.Fetch("hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.IsNil)
	c.Assert(path, qt.Not(qt.Equals), "")

	runner := &mockRunner{}
	l.runner = runner
	err = l.Run(ctx, "hello", "hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.IsNil)
	c.Assert(runner.runs, qt.HasLen, 1)
	c.Assert(strings.Join(runner.runs[0], " "), qt.Matches,
		`/usr/local/lib/node_modules/.bin/sweater-comb bulk-compare --input /.*/.*-input.json`)

	// Command failed.
	runner = &mockRunner{err: fmt.Errorf("bad wolf")}
	l.runner = runner
	err = l.Run(ctx, "hello", "hello/2021-06-01/spec.yaml")
	c.Assert(err, qt.ErrorMatches, ".*: bad wolf")
}

func TestMatchDisjointSources(t *testing.T) {
	c := qt.New(t)
	o := &Optic{
		fromSource: mockSource([]string{"apple", "orange"}),
		toSource:   mockSource([]string{"blue", "green"}),
	}
	result, err := o.Match(&config.ResourceSet{Path: "whatever"})
	c.Assert(err, qt.IsNil)
	c.Assert(result, qt.ContentEquals, []string{"apple", "blue", "green", "orange"})
}

func TestMatchIntersectSources(t *testing.T) {
	c := qt.New(t)
	o := &Optic{
		fromSource: mockSource([]string{"apple", "orange"}),
		toSource:   mockSource([]string{"orange", "green"}),
	}
	result, err := o.Match(&config.ResourceSet{Path: "whatever"})
	c.Assert(err, qt.IsNil)
	c.Assert(result, qt.ContentEquals, []string{"apple", "green", "orange"})
}

type mockRunner struct {
	runs [][]string
	err  error
}

func (r *mockRunner) run(cmd *exec.Cmd) error {
	fmt.Fprintln(cmd.Stdout, "/from/here.yaml /to/eternity.yaml")
	r.runs = append(r.runs, cmd.Args)
	return r.err
}

func copyFile(c *qt.C, dst, src string) {
	contents, err := ioutil.ReadFile(src)
	c.Assert(err, qt.IsNil)
	err = ioutil.WriteFile(dst, contents, 0644)
	c.Assert(err, qt.IsNil)
}

func setupGitRepo(c *qt.C) (string, plumbing.Hash) {
	testRepo := c.TempDir()
	repo, err := git.PlainInit(testRepo, false)
	c.Assert(err, qt.IsNil)
	versionDir := testRepo + "/hello/2021-06-01"
	c.Assert(os.MkdirAll(versionDir, 0777), qt.IsNil)
	copyFile(c, filepath.Join(versionDir, "spec.yaml"), testdata.Path("resources/_examples/hello-world/2021-06-01/spec.yaml"))
	worktree, err := repo.Worktree()
	c.Assert(err, qt.IsNil)
	_, err = worktree.Add("hello")
	c.Assert(err, qt.IsNil)
	commitHash, err := worktree.Commit("test: initial commit", &git.CommitOptions{
		All: true,
		Author: &object.Signature{
			Name:  "Bob Dobbs",
			Email: "bob@example.com",
		},
	})
	c.Assert(err, qt.IsNil)
	copyFile(c, filepath.Join(versionDir, "spec.yaml"), testdata.Path("resources/_examples/hello-world/2021-06-13/spec.yaml"))
	return testRepo, commitHash
}

type mockSource []string

func (m mockSource) Name() string {
	return "mock"
}

func (m mockSource) Match(*config.ResourceSet) ([]string, error) {
	return m, nil
}

func (mockSource) Prefetch(path string) (string, error) { return path, nil }

func (mockSource) Fetch(path string) (string, error) { return path, nil }

func (mockSource) Close() error { return nil }
