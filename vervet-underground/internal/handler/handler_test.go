package handler_test

import (
	"context"
	"io/ioutil"
	"net/http/httptest"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"github.com/snyk/vervet/v4"

	"vervet-underground/config"
	"vervet-underground/internal/handler"
	"vervet-underground/internal/scraper"
)

func TestHealth(t *testing.T) {
	c := qt.New(t)
	cfg, h := setup(c)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(w, req)
	c.Assert(w.Code, qt.Equals, 200)
	contents, err := ioutil.ReadAll(w.Result().Body)
	c.Assert(err, qt.IsNil)
	c.Assert(contents, qt.JSONEquals, map[string]interface{}{
		"msg":      "success",
		"services": cfg.Services,
	})
}

func TestOpenapi(t *testing.T) {
	c := qt.New(t)
	_, h := setup(c)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi", nil)
	h.ServeHTTP(w, req)
	c.Assert(w.Code, qt.Equals, 200)
	contents, err := ioutil.ReadAll(w.Result().Body)
	c.Assert(err, qt.IsNil)
	c.Assert(contents, qt.JSONEquals, []string{
		"2021-06-04~experimental",
		"2021-10-20~experimental",
		"2021-10-20~beta",
		"2022-01-16~experimental",
		"2022-01-16~beta",
		"2022-01-16",
	})
}

func TestOpenapiVersion(t *testing.T) {
	c := qt.New(t)
	_, h := setup(c)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi/2022-01-16~beta", nil)
	h.ServeHTTP(w, req)
	c.Assert(w.Code, qt.Equals, 200)
	contents, err := ioutil.ReadAll(w.Result().Body)
	c.Assert(err, qt.IsNil)
	c.Assert(contents, qt.DeepEquals, []byte("got 2022-01-16~beta"))
}

func TestOpenapiVersionNotFound(t *testing.T) {
	c := qt.New(t)
	_, h := setup(c)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi/2021-01-16~beta", nil)
	h.ServeHTTP(w, req)
	c.Assert(w.Code, qt.Equals, 404)
	contents, err := ioutil.ReadAll(w.Result().Body)
	c.Assert(err, qt.IsNil)
	c.Assert(contents, qt.DeepEquals, []byte("Version not found\n"))
}

func setup(c *qt.C) (*config.ServerConfig, *handler.Handler) {
	cfg := &config.ServerConfig{
		Services: []config.ServiceConfig{{
			Name: "petfood", URL: "http://petfood.svc.cluster.local",
		}, {
			Name: "animals", URL: "http://animals.svc.cluster.local",
		}},
	}
	st := &mockStorage{}
	sc, err := scraper.New(cfg, st)
	c.Assert(err, qt.IsNil)
	h := handler.New(cfg, sc)
	return cfg, h
}

type mockStorage struct{}

func (s *mockStorage) NotifyVersions(ctx context.Context, name string, versions []string, scrapeTime time.Time) error {
	return nil
}

func (s *mockStorage) CollateVersions(ctx context.Context, serviceFilter map[string]bool) error {
	return nil
}

func (s *mockStorage) HasVersion(ctx context.Context, name string, version string, digest string) (bool, error) {
	return true, nil
}

func (s *mockStorage) NotifyVersion(ctx context.Context, name string, version string, contents []byte, scrapeTime time.Time) error {
	return nil
}

func (s *mockStorage) Versions() vervet.VersionSlice {
	return vervet.VersionSlice{
		vervet.MustParseVersion("2021-06-04~experimental"),
		vervet.MustParseVersion("2021-10-20~experimental"),
		vervet.MustParseVersion("2021-10-20~beta"),
		vervet.MustParseVersion("2022-01-16~experimental"),
		vervet.MustParseVersion("2022-01-16~beta"),
		vervet.MustParseVersion("2022-01-16~ga"),
	}
}

func (s *mockStorage) Version(ctx context.Context, version string) ([]byte, error) {
	return []byte("got " + version), nil
}