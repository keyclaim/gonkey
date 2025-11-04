package allure_report

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lamoda/gonkey/models"
	"github.com/lamoda/gonkey/output/allure_report/allure2"
)

// Allure2Output generates Allure 2 JSON format reports
type Allure2Output struct {
	reportLocation   string
	defaultPackage   string
	defaultTestClass string
}

// NewAllure2Output creates a new Allure 2 output handler
func NewAllure2Output(reportLocation string) *Allure2Output {
	resultsDir, _ := filepath.Abs(reportLocation)
	_ = os.MkdirAll(resultsDir, 0755)

	return &Allure2Output{
		reportLocation: resultsDir,
	}
}

// WithDefaultLabels sets default package and testClass labels
func (o *Allure2Output) WithDefaultLabels(packageLabel, testClassLabel string) *Allure2Output {
	o.defaultPackage = packageLabel
	o.defaultTestClass = testClassLabel
	return o
}

func (o *Allure2Output) Process(t models.TestInterface, result *models.Result) error {
	allureResult := allure2.NewResult(t.GetName(), o.reportLocation)

	if desc := t.GetDescription(); desc != "" {
		allureResult.WithDescription(desc)
	}

	status, err := result.AllureStatus()
	allureResult.WithStatus(status)

	if err != nil {
		allureResult.WithStatusDetails(err.Error(), "")
	}

	hasPackageLabel := false
	hasTestClassLabel := false

	if metadata := t.GetAllureMetadata(); metadata != nil {
		for _, label := range metadata.Labels {
			allureResult.AddLabel(label.Name, label.Value)
			if label.Name == "package" {
				hasPackageLabel = true
			}
			if label.Name == "testClass" {
				hasTestClassLabel = true
			}
		}

		for _, link := range metadata.Links {
			allureResult.AddLink(link.Name, link.URL, link.Type)
		}

		for _, param := range metadata.Parameters {
			allureResult.AddParameter(param.Name, param.Value)
		}
	}

	if !hasPackageLabel && o.defaultPackage != "" {
		allureResult.AddLabel("package", o.defaultPackage)
	}
	if !hasTestClassLabel && o.defaultTestClass != "" {
		allureResult.AddLabel("testClass", o.defaultTestClass)
	}

	allureResult.AddLabel(allure2.LabelFramework, "gonkey")
	allureResult.AddLabel(allure2.LabelLanguage, "go")

	if result.Path != "" {
		allureResult.AddLabel(allure2.LabelStory, result.Path)
	}

	if len(t.GetCombinedVariables()) > 0 {
		for k, v := range t.GetCombinedVariables() {
			allureResult.AddParameter(k, v)
		}
	}

	o.addPreparationStep(allureResult, t)
	if err := o.addRequestStep(allureResult, t, result); err != nil {
		return fmt.Errorf("failed to add request step: %w", err)
	}
	if err := o.addVerificationStep(allureResult, t, result); err != nil {
		return fmt.Errorf("failed to add verification step: %w", err)
	}

	allureResult.Finish()
	if err := allureResult.Save(); err != nil {
		return fmt.Errorf("failed to save allure result: %w", err)
	}

	return nil
}

func (o *Allure2Output) addPreparationStep(result *allure2.Result, t models.TestInterface) {
	hasFixtures := len(t.Fixtures()) > 0 || len(t.FixturesMultiDb()) > 0
	hasMocks := len(t.ServiceMocks()) > 0

	if !hasFixtures && !hasMocks {
		return
	}

	prepStep := result.StartStep("Подготовка тестовых данных")

	if hasFixtures {
		fixturesInfo := formatFixturesInfo(t.Fixtures(), t.FixturesMultiDb())
		if fixturesInfo != "" {
			fixtureStep := prepStep.StartSubStep("Загрузка фикстур в БД")
			fixtureStep.AddParameter("files", fixturesInfo)
			fixtureStep.Finish(allure2.StatusPassed)
		}
	}

	if hasMocks {
		mocksInfo := extractMocksInfo(t.ServiceMocks())
		if len(mocksInfo) > 0 {
			mocksStep := prepStep.StartSubStep("Настройка mock-сервисов")

			for _, mockInfo := range mocksInfo {
				serviceStep := mocksStep.StartSubStep(mockInfo.ServiceName)
				serviceStep.AddParameter("strategy", mockInfo.Strategy)

				if len(mockInfo.Endpoints) > 0 {
					endpointsStr := fmt.Sprintf("%d: %s", len(mockInfo.Endpoints),
						joinEndpoints(mockInfo.Endpoints))
					serviceStep.AddParameter("endpoints", endpointsStr)
				}

				serviceStep.Finish(allure2.StatusPassed)
			}

			mocksStep.Finish(allure2.StatusPassed)
		}
	}

	prepStep.Finish(allure2.StatusPassed)
}

func (o *Allure2Output) addRequestStep(result *allure2.Result, t models.TestInterface, testResult *models.Result) error {
	stepName := fmt.Sprintf("Отправка %s запроса к %s", t.GetMethod(), testResult.Path)
	requestStep := result.StartStep(stepName)

	requestStep.AddParameter("method", t.GetMethod())
	if testResult.Query != "" {
		requestStep.AddParameter("query", testResult.Query)
	}

	if testResult.RequestBody != "" {
		if err := requestStep.AddAttachment("Request Body", testResult.RequestBody,
			allure2.MimeTypeApplicationJSON, o.reportLocation); err != nil {
			return err
		}
	}

	if len(t.Headers()) > 0 {
		headersContent := formatHeaders(t.Headers())
		if err := requestStep.AddAttachment("Request Headers", headersContent,
			allure2.MimeTypeTextPlain, o.reportLocation); err != nil {
			return err
		}
	}

	if len(t.Cookies()) > 0 {
		cookiesContent := formatCookies(t.Cookies())
		if err := requestStep.AddAttachment("Request Cookies", cookiesContent,
			allure2.MimeTypeTextPlain, o.reportLocation); err != nil {
			return err
		}
	}

	requestStep.Finish(allure2.StatusPassed)
	return nil
}

func (o *Allure2Output) addVerificationStep(result *allure2.Result, t models.TestInterface, testResult *models.Result) error {
	verifyStep := result.StartStep("Проверка ответа сервера")

	errorCategories := categorizeErrors(testResult.Errors)
	hasStatusCodeError := len(errorCategories[models.ErrorCategoryStatusCode]) > 0
	hasBodyError := len(errorCategories[models.ErrorCategoryResponseBody]) > 0
	hasHeaderError := len(errorCategories[models.ErrorCategoryResponseHeader]) > 0
	dbErrors := errorCategories[models.ErrorCategoryDatabase]
	mockErrors := errorCategories[models.ErrorCategoryMock]

	verificationStatus := allure2.StatusPassed
	if !testResult.Passed() {
		verificationStatus = allure2.StatusFailed
	}

	statusCodeStatus := allure2.StatusPassed
	if hasStatusCodeError {
		statusCodeStatus = allure2.StatusFailed
	}

	expectedCodes := getExpectedStatusCodes(t.GetResponses())
	statusStepName := fmt.Sprintf("Проверка статус кода")
	if len(expectedCodes) > 0 {
		statusStepName = fmt.Sprintf("Проверка статус кода (ожидается: %s)", formatStatusCodes(expectedCodes))
	}

	statusStep := verifyStep.StartSubStep(statusStepName)
	statusStep.AddParameter("actual", fmt.Sprintf("%d", testResult.ResponseStatusCode))
	statusStep.Finish(statusCodeStatus)

	bodyStepStatus := allure2.StatusPassed
	if hasBodyError {
		bodyStepStatus = allure2.StatusFailed
	}

	bodyStep := verifyStep.StartSubStep("Проверка структуры JSON ответа")
	if testResult.ResponseBody != "" {
		if err := bodyStep.AddAttachment("Response Body", testResult.ResponseBody,
			allure2.MimeTypeApplicationJSON, o.reportLocation); err != nil {
			return err
		}
	}
	bodyStep.Finish(bodyStepStatus)

	expectedHeaders, hasExpectedHeaders := t.GetResponseHeaders(testResult.ResponseStatusCode)
	if hasExpectedHeaders && len(expectedHeaders) > 0 {
		headerStepStatus := allure2.StatusPassed
		if hasHeaderError {
			headerStepStatus = allure2.StatusFailed
		}

		headerStep := verifyStep.StartSubStep("Проверка заголовков ответа")
		if len(testResult.ResponseHeaders) > 0 {
			headersContent := formatResponseHeaders(testResult.ResponseHeaders)
			if err := headerStep.AddAttachment("Response Headers", headersContent,
				allure2.MimeTypeTextPlain, o.reportLocation); err != nil {
				return err
			}
		}
		headerStep.Finish(headerStepStatus)
	}

	if len(testResult.DatabaseResult) > 0 {
		dbStep := verifyStep.StartSubStep("Проверка данных в БД")

		for i, dbResult := range testResult.DatabaseResult {
			if dbResult.Query != "" {
				queryIdentifier := fmt.Sprintf("%d", i)
				queryHasError := len(dbErrors[queryIdentifier]) > 0
				queryStepStatus := allure2.StatusPassed
				if queryHasError {
					queryStepStatus = allure2.StatusFailed
				}

				dbQueryStep := dbStep.StartSubStep(fmt.Sprintf("DB Query #%d", i+1))

				queryContent := fmt.Sprintf("SQL: %s", dbResult.Query)
				if err := dbQueryStep.AddAttachment("Query", queryContent,
					allure2.MimeTypeTextPlain, o.reportLocation); err != nil {
					return err
				}

				if len(dbResult.Response) > 0 {
					responseContent := formatDbResponse(dbResult.Response)
					if err := dbQueryStep.AddAttachment("Response", responseContent,
						allure2.MimeTypeTextPlain, o.reportLocation); err != nil {
						return err
					}
				}

				dbQueryStep.Finish(queryStepStatus)
			}
		}

		dbStepStatus := allure2.StatusPassed
		if len(dbErrors) > 0 {
			dbStepStatus = allure2.StatusFailed
		}
		dbStep.Finish(dbStepStatus)
	}

	if len(t.ServiceMocks()) > 0 {
		mockStep := verifyStep.StartSubStep("Проверка вызовов mock-сервисов")

		mocksInfo := extractMocksInfo(t.ServiceMocks())
		for _, mockInfo := range mocksInfo {
			serviceHasError := len(mockErrors[mockInfo.ServiceName]) > 0
			serviceStepStatus := allure2.StatusPassed
			if serviceHasError {
				serviceStepStatus = allure2.StatusFailed
			}

			serviceStep := mockStep.StartSubStep(fmt.Sprintf("Mock: %s", mockInfo.ServiceName))
			serviceStep.AddParameter("strategy", mockInfo.Strategy)
			if len(mockInfo.Endpoints) > 0 {
				serviceStep.AddParameter("endpoints", joinEndpoints(mockInfo.Endpoints))
			}
			serviceStep.Finish(serviceStepStatus)
		}

		mockStepStatus := allure2.StatusPassed
		if len(mockErrors) > 0 {
			mockStepStatus = allure2.StatusFailed
		}
		mockStep.Finish(mockStepStatus)
	}

	verifyStep.Finish(verificationStatus)
	return nil
}

func joinEndpoints(endpoints []string) string {
	if len(endpoints) > 3 {
		return fmt.Sprintf("%s, ... (%d total)", endpoints[0], len(endpoints))
	}
	return strings.Join(endpoints, ", ")
}

func formatHeaders(headers map[string]string) string {
	var lines []string
	for k, v := range headers {
		lines = append(lines, fmt.Sprintf("%s: %s", k, v))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func formatResponseHeaders(headers map[string][]string) string {
	var lines []string
	for k, values := range headers {
		for _, v := range values {
			lines = append(lines, fmt.Sprintf("%s: %s", k, v))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func formatCookies(cookies map[string]string) string {
	var lines []string
	for k, v := range cookies {
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(lines, "\n")
}

func formatDbResponse(response []string) string {
	if len(response) == 0 {
		return "[]"
	}
	if len(response) > 10 {
		return fmt.Sprintf("[\n  %s,\n  ... (%d total rows)\n]",
			response[0], len(response))
	}
	return fmt.Sprintf("[\n  %s\n]", strings.Join(response, ",\n  "))
}

func getExpectedStatusCodes(responses map[int]string) []int {
	codes := make([]int, 0, len(responses))
	for code := range responses {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

func formatStatusCodes(codes []int) string {
	if len(codes) == 0 {
		return ""
	}
	if len(codes) == 1 {
		return fmt.Sprintf("%d", codes[0])
	}
	strCodes := make([]string, len(codes))
	for i, code := range codes {
		strCodes[i] = fmt.Sprintf("%d", code)
	}
	return strings.Join(strCodes, ", ")
}

type ErrorsByIdentifier map[string][]error

func categorizeErrors(errs []error) map[models.ErrorCategory]ErrorsByIdentifier {
	categories := make(map[models.ErrorCategory]ErrorsByIdentifier)

	for _, err := range errs {
		var checkErr *models.CheckError
		if errors.As(err, &checkErr) {
			category := checkErr.GetCategory()
			identifier := checkErr.GetIdentifier()

			if categories[category] == nil {
				categories[category] = make(ErrorsByIdentifier)
			}
			categories[category][identifier] = append(categories[category][identifier], err)
		} else {
			// Uncategorized errors are treated as body errors for backward compatibility
			if categories[models.ErrorCategoryResponseBody] == nil {
				categories[models.ErrorCategoryResponseBody] = make(ErrorsByIdentifier)
			}
			categories[models.ErrorCategoryResponseBody][""] = append(categories[models.ErrorCategoryResponseBody][""], err)
		}
	}

	return categories
}

func (o *Allure2Output) Finalize() {
}
