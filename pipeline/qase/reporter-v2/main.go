package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/rancher/apiserver-tests/extensions/qase"
	qaseactions "github.com/rancher/apiserver-tests/extensions/qase"
	"github.com/rancher/apiserver-tests/extensions/qase/testresult"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

var (
	runIDEnvVar             = os.Getenv(qase.TestRunEnvVar)
	projectIDEnvVar         = os.Getenv(qase.ProjectIDEnvVar)
	testRunName             = os.Getenv(qase.TestRunNameEnvVar)
	testRunComplete         = os.Getenv(qase.TestRunCompleteEnvVar)
	schemaPrefixEnvVar      = os.Getenv(qase.SchemaPrefixEnvVar)
	customFieldFilterEnvVar = os.Getenv("QASE_CUSTOM_FIELD_FILTER")
	buildUrl                = os.Getenv(qase.BuildUrl)
	_, callerFilePath, _, _ = runtime.Caller(0)
	basepath                = filepath.Join(filepath.Dir(callerFilePath), "..", "..", "..")
	validStatus             = map[string]string{"pass": "passed", "fail": "failed", "skip": "skipped"}
	rancherTestCommitID     = os.Getenv(qase.RancherTestCommitID)
)

const (
	// Doc: https://developers.qase.io/reference/create-run
	descriptionLimit = 10000
	imageReportPath  = "/app/image-report/image-report.txt"
	testResultsJSON  = "results.json"
)

func main() {
	logrus.Info("Running QASE reporter v2")

	if projectIDEnvVar == "" {
		logrus.Warningf(
			"Project env var not provided, defaulting to %s",
			qaseactions.RancherManagerProjectID,
		)
		projectIDEnvVar = qaseactions.RancherManagerProjectID
	}

	if runIDEnvVar != "" || testRunName != "" {
		qaseService := qase.SetupQaseClient()

		runID := int64(0)
		err := error(nil)

		if runIDEnvVar != "" {
			runID, err = strconv.ParseInt(runIDEnvVar, 10, 64)
		}

		runDescription := createRunDescription(buildUrl, rancherTestCommitID)

		if testRunName != "" {
			resp, err := qaseService.CreateTestRun(
				testRunName,
				projectIDEnvVar,
				runDescription,
			)
			if err != nil {
				logrus.Error("error creating test run: ", err)
			} else {
				runID = *resp.Result.Id
			}
		}

		if err != nil {
			logrus.Fatalf(
				"error reporting converting string to int64: %v",
				err,
			)
		}

		err = reportTestQases(qaseService, int32(runID))
		if err != nil {
			logrus.Error("error reporting: ", err)
		}

		isCompleteRun, _ := strconv.ParseBool(testRunComplete)
		if isCompleteRun {
			err = qaseService.CompleteTestRun(
				projectIDEnvVar,
				int32(runID),
			)
			if err != nil {
				logrus.Error("error update reporting: ", err)
			}
		}
	} else {
		logrus.Warning("QASE run ID not provided")
	}
}

// findQaseTestCase searches Qase for a test case matching the Go test name.
func findQaseTestCase(
	qaseService *qase.Service,
	testName string,
) (*upstream.TestCase, error) {
	casesRequest := qaseService.Client.CasesAPI.GetCases(
		context.TODO(),
		projectIDEnvVar,
	)

	casesRequest = casesRequest.Search(testName)

	resp, _, err := casesRequest.Execute()
	if err != nil {
		return nil, fmt.Errorf(
			"failed to search Qase cases for %q: %w",
			testName,
			err,
		)
	}

	for _, testCase := range resp.Result.Entities {
		if testCase.Title == nil || *testCase.Title != testName {
			continue
		}

		if customFieldFilterEnvVar != "" &&
			!hasCustomFieldValue(
				testCase.CustomFields,
				customFieldFilterEnvVar,
			) {
			continue
		}

		return &testCase, nil
	}

	return nil, fmt.Errorf(
		"test case not found in Qase: %s",
		testName,
	)
}

// readTestResults converts the results.json file into an output object.
func readTestResults() ([]testresult.GoTestOutput, error) {
	logrus.Infof(
		"Reading test results from %s",
		qase.TestResultsJSON,
	)

	file, err := os.Open(qase.TestResultsJSON)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to open %s: %w",
			qase.TestResultsJSON,
			err,
		)
	}
	defer file.Close()

	fscanner := bufio.NewScanner(file)
	outputLines := []testresult.GoTestOutput{}
	lineNumber := 0

	for fscanner.Scan() {
		lineNumber++

		var testCase testresult.GoTestOutput

		err = yaml.Unmarshal(
			fscanner.Bytes(),
			&testCase,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to parse %s line %d: %w",
				qase.TestResultsJSON,
				lineNumber,
				err,
			)
		}

		outputLines = append(outputLines, testCase)
	}

	if err := fscanner.Err(); err != nil {
		return nil, fmt.Errorf(
			"failed reading %s: %w",
			qase.TestResultsJSON,
			err,
		)
	}

	logrus.Infof(
		"Read %d result lines from %s",
		len(outputLines),
		qase.TestResultsJSON,
	)

	return outputLines, nil
}

// parseTestResults parses the results.json into a test results object.
func parseTestResults(
	outputs []testresult.GoTestOutput,
) map[string]*testresult.GoTestResult {
	finalTestResults := map[string]*testresult.GoTestResult{}
	var timeoutFailure bool

	for _, output := range outputs {
		tableTestName := output.Test

		if tableTestName != "" {
			testName := strings.Split(tableTestName, "/")
			tableTestName = testName[len(testName)-1]
		}

		if output.Action == "run" && tableTestName != "" {
			newTestResult := &testresult.GoTestResult{
				Name:    tableTestName,
				Package: output.Package,
			}

			finalTestResults[tableTestName] = newTestResult

		} else if output.Action == "output" && tableTestName != "" {
			if goTestResult, ok := finalTestResults[tableTestName]; ok {
				goTestResult.StackTrace += output.Output
			}

		} else if (output.Action == qase.FailStatus ||
			output.Action == qase.PassStatus ||
			output.Action == qase.SkipStatus) &&
			tableTestName != "" {

			if goTestResult, ok := finalTestResults[tableTestName]; ok {
				goTestResult.StackTrace += output.Output
				goTestResult.Status = output.Action
				goTestResult.Elapsed = output.Elapsed
			}

			if output.Action == qase.FailStatus {
				timeoutFailure = true
			}
		}
	}

	for _, testResult := range finalTestResults {
		testSuite := strings.Split(testResult.Name, "/")
		testName := testSuite[len(testSuite)-1]

		testResult.Name = testName
		testResult.TestSuite = testSuite[0 : len(testSuite)-1]

		if timeoutFailure && testResult.Status == "" {
			testResult.Status = qase.FailStatus
		}
	}

	logrus.Infof(
		"Parsed %d Go test results",
		len(finalTestResults),
	)

	return finalTestResults
}

// reportTestQases updates a Qase test run with the results of a set of tests.
func reportTestQases(
	qaseService *qase.Service,
	testRunID int32,
) error {
	resultsOutputs, err := readTestResults()
	if err != nil {
		return err
	}

	goTestResults := parseTestResults(resultsOutputs)

	logrus.Infof(
		"Reporting %d Go test results to Qase",
		len(goTestResults),
	)

	var reportErrors []string

	for _, goTestResult := range goTestResults {
		testQase, err := findQaseTestCase(
			qaseService,
			goTestResult.Name,
		)
		if err != nil {
			logrus.Warnf(
				"Qase case lookup failed for %q: %v",
				goTestResult.Name,
				err,
			)

			reportErrors = append(
				reportErrors,
				err.Error(),
			)

			continue
		}

		var params []upstream.TestCaseParameterCreate

		schemaPath := filepath.Join(basepath, "schemas")

		qaseProjects, err := qase.GetSchemasByPrefix(
			schemaPath,
			schemaPrefixEnvVar,
		)
		if err != nil {
			logrus.Warnf(
				"Schema lookup failed for %q; reporting without parameters: %v",
				goTestResult.Name,
				err,
			)
		} else {
			qaseTestSchema, err := qase.GetTestSchema(
				goTestResult.Name,
				qaseProjects,
			)
			if err != nil {
				logrus.Warnf(
					"Test %q not found in schema; reporting without parameters",
					goTestResult.Name,
				)
			} else {
				params = qaseTestSchema.Parameters
			}
		}

		err = updateTestInRun(
			qaseService.Client,
			*goTestResult,
			*testQase,
			params,
			testRunID,
		)
		if err != nil {
			logrus.Warnf(
				"Failed to report %q to Qase case: %v",
				goTestResult.Name,
				err,
			)

			reportErrors = append(
				reportErrors,
				err.Error(),
			)

			continue
		}

		status := validStatus[goTestResult.Status]
		if status == "" {
			status = "failed"
		}

		logrus.Infof(
			"Reported %q to Qase case (%s)",
			goTestResult.Name,
			status,
		)
	}

	if len(reportErrors) > 0 {
		return fmt.Errorf(
			"failed to report %d test(s) to Qase",
			len(reportErrors),
		)
	}

	logrus.Infof(
		"Successfully reported %d Go tests to Qase",
		len(goTestResults),
	)

	return nil
}

// updateTestInRun updates the current Qase test run with a test.
func updateTestInRun(
	client *upstream.APIClient,
	testResult testresult.GoTestResult,
	qaseTestCase upstream.TestCase,
	params []upstream.TestCaseParameterCreate,
	testRunID int32,
) error {
	var elapsedTime int64

	if testResult.Elapsed != "" {
		floatTime, err := strconv.ParseFloat(
			testResult.Elapsed,
			64,
		)
		if err != nil {
			return err
		}

		elapsedTime = int64(floatTime)
	}

	resultParams := make(map[string]string)

	for _, param := range params {
		if param.ParameterSingle == nil {
			continue
		}

		if len(param.ParameterSingle.Values) > 0 {
			paramKey := param.ParameterSingle.Title
			paramVal := strings.Join(
				param.ParameterSingle.Values,
				", ",
			)

			if paramVal == "" {
				continue
			}

			resultParams[paramKey] = paramVal
		}
	}

	status, exists := validStatus[testResult.Status]
	if !exists {
		status = "failed"
	}

	resultBody := upstream.ResultCreate{
		CaseId: qaseTestCase.Id,
		Status: status,
		Time: *upstream.NewNullableInt64(
			&elapsedTime,
		),
		Param: resultParams,
		Comment: *upstream.NewNullableString(
			&testResult.StackTrace,
		),
	}

	resultRequest := client.ResultsAPI.CreateResult(
		context.TODO(),
		projectIDEnvVar,
		testRunID,
	)

	resultRequest = resultRequest.ResultCreate(resultBody)

	_, _, err := resultRequest.Execute()
	if err != nil {
		return err
	}

	return nil
}

// getAutomationTestName gets the custom test name field.
func getAutomationTestName(
	customFields []upstream.CustomFieldValue,
) string {
	for _, field := range customFields {
		if field.Id != nil && *field.Id == qase.AutomationTestNameID {
			if field.Value != nil {
				return *field.Value
			}
		}
	}

	return ""
}

func hasCustomFieldValue(
	customFields []upstream.CustomFieldValue,
	expectedValue string,
) bool {
	for _, field := range customFields {
		if field.Value != nil &&
			*field.Value == expectedValue {
			return true
		}
	}

	return false
}

// createRunDescription builds the Qase test run description.
func createRunDescription(
	buildUrl string,
	commitId string,
) string {
	var description strings.Builder

	if buildUrl != "" {
		description.WriteString("Jenkins Job")
		description.WriteString("\n")
		description.WriteString(buildUrl)
		description.WriteString("\n")
	}

	if commitId != "" {
		description.WriteString("Rancher Test Commit ID")
		description.WriteString("\n")
		description.WriteString(commitId)
		description.WriteString("\n")
	}

	versions := getVersionInformation()
	if versions != "" {
		if description.Len() > 0 {
			description.WriteString("\n")
		}

		description.WriteString(versions)
	}

	s := description.String()

	if len(s) > descriptionLimit {
		s = s[:descriptionLimit]
	}

	return s
}

// getVersionInformation gets versions and commit id from cluster.
func getVersionInformation() string {
	data, err := os.ReadFile(imageReportPath)
	if err != nil {
		logrus.Warningf(
			"Failed to read file: %v",
			err,
		)
	}

	return string(data)
}
