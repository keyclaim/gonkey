package runner

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/keyclaim/gonkey/checker/response_body"
	"github.com/keyclaim/gonkey/checker/response_db"
	"github.com/keyclaim/gonkey/checker/response_header"
	"github.com/keyclaim/gonkey/fixtures"
	"github.com/keyclaim/gonkey/mocks"
	"github.com/keyclaim/gonkey/output/allure_report"
	testingOutput "github.com/keyclaim/gonkey/output/testing"
	"github.com/keyclaim/gonkey/testloader/yaml_file"
	"github.com/keyclaim/gonkey/variables"
)

type RunWithTestingParams struct {
	Server      *httptest.Server
	TestsDir    string
	Mocks       *mocks.Mocks
	FixturesDir string
	DB          *sql.DB
	Driver      string
}

// RunWithTesting is a helper function the wraps the common Run and provides simple way
// to configure Gonkey by filling the params structure.
func RunWithTesting(t *testing.T, params *RunWithTestingParams) {
	var mocksLoader *mocks.Loader
	if params.Mocks != nil {
		mocksLoader = mocks.NewLoader(params.Mocks)
	}

	debug := os.Getenv("GONKEY_DEBUG") != ""

	var fixturesLoader *fixtures.Loader
	var err error
	if params.DB != nil {
		fixturesLoader, err = fixtures.NewLoader(&fixtures.Config{
			Location: params.FixturesDir,
			DB:       params.DB,
			Driver:   params.Driver,
			Debug:    debug,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	yamlLoader := yaml_file.NewLoader(params.TestsDir)
	yamlLoader.SetFileFilter(os.Getenv("GONKEY_FILE_FILTER"))

	r := New(
		&Config{
			Host:           params.Server.URL,
			Mocks:          params.Mocks,
			MocksLoader:    mocksLoader,
			FixturesLoader: fixturesLoader,
			Variables:      variables.New(),
		},
		yamlLoader,
	)

	r.AddOutput(testingOutput.NewOutput(t))

	if os.Getenv("GONKEY_ALLURE_DIR") != "" {
		allureOutput := allure_report.NewOutput(strings.TrimPrefix(params.TestsDir, "./cases/"), os.Getenv("GONKEY_ALLURE_DIR"))
		defer allureOutput.Finalize()
		r.AddOutput(allureOutput)
	}

	r.AddCheckers(response_body.NewChecker())
	r.AddCheckers(response_header.NewChecker())

	if params.DB != nil {
		r.AddCheckers(response_db.NewChecker(params.DB))
	}

	_, err = r.Run()
	if err != nil {
		t.Fatal(err)
	}
}
