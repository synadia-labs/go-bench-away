package reports

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/synadia-labs/go-bench-away/v1/core"
)

const (
	job1 = "067997a3-761e-475e-9559-f10d7400b835"
	job2 = "dd146049-0137-4ba0-89b1-0a2f8d0a2268"
	job3 = "e98b2caa-df6d-4f12-815c-431db896a9f5"
)

type mockClient struct {
}

func (m mockClient) LoadJob(jobId string) (*core.JobRecord, uint64, error) {
	recordPath := filepath.Join("testdata", fmt.Sprintf("%s.json", jobId))

	file, err := os.Open(recordPath)
	if err != nil {
		panic(err)
	}

	jr := &core.JobRecord{}

	err = json.NewDecoder(file).Decode(jr)
	if err != nil {
		panic(err)
	}

	return jr, 1, nil
}

func (m mockClient) LoadResultsArtifact(record *core.JobRecord, writer io.Writer) error {

	resultsPath := filepath.Join("testdata", fmt.Sprintf("%s_results.txt", record.Id))

	file, err := os.Open(resultsPath)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = io.Copy(writer, file)
	if err != nil {
		panic(err)
	}

	return nil
}

func TestWriteEmptyReport(t *testing.T) {
	resetChartId()
	cfg := ReportConfig{
		Title:   "Empty report",
		verbose: true,
	}

	writeReportAndCompareToExpected(t, []string{job1, job2, job3}, &cfg, "empty.html")
}

func TestWriteSingleResultSetReport(t *testing.T) {
	resetChartId()

	tests := []string{
		"single1",
	}

	for _, test := range tests {
		specName := test + ".json"
		reportName := test + ".html"
		t.Run(
			"Custom report: "+specName+" -> "+reportName,
			func(t *testing.T) {
				specPath := filepath.Join("testconfig", specName)

				var spec ReportSpec
				err := spec.LoadFile(specPath)
				if err != nil {
					t.Fatal(err)
				}

				cfg := &ReportConfig{}
				err = spec.ConfigureReport(cfg)
				if err != nil {
					t.Fatal(err)
				}

				writeReportAndCompareToExpected(t, []string{job1}, cfg, reportName)
			},
		)
	}
}

func TestWriteTrendAndBarsReport(t *testing.T) {

	testCases := []struct {
		metric   Metric
		filename string
	}{
		{TimeOp, "trend_and_bars_timeop.html"},
		{Speed, "trend_and_bars_speed.html"},
		{Throughput, "trend_and_bars_tput.html"},
		{OpsPerSec, "trend_and_bars_opss.html"},
		{MsgPerSec, "trend_and_bars_msgs.html"},
	}

	const filter = ".*KV/N=3.*(PUT|GET)"

	for _, testCase := range testCases {
		metricString := string(testCase.metric)
		t.Run(
			metricString,
			func(t *testing.T) {
				resetChartId()
				cfg := &ReportConfig{
					Title:   "Trend, bar and table report for metric " + metricString,
					verbose: true,
				}

				cfg.AddSections(
					JobsTable(),
					TrendChart("Trend chart: "+metricString, testCase.metric, filter),
					HorizontalBarChart("Bar chart: "+metricString, testCase.metric, filter),
					ResultsTable(testCase.metric, filter, true),
				)

				writeReportAndCompareToExpected(t, []string{job1, job2, job3}, cfg, testCase.filename)

			},
		)
	}
}

func TestWriteTrendReportFiltered(t *testing.T) {
	resetChartId()
	cfg := &ReportConfig{
		Title:   "Trend report",
		verbose: true,
	}

	filter := ".*JetStreamKV/.*/CAS"

	cfg.AddSections(
		JobsTable(),
		TrendChart("", TimeOp, filter),
		ResultsTable(TimeOp, filter, true),
		TrendChart("", Speed, filter),
		ResultsTable(Speed, filter, true),
	)

	writeReportAndCompareToExpected(t, []string{job1, job2, job3}, cfg, "trend_filtered.html")
}

func TestWriteCompareNReport(t *testing.T) {
	resetChartId()
	cfg := &ReportConfig{
		Title:   "Comparative report",
		verbose: true,
	}

	filter := ""

	cfg.AddSections(
		JobsTable(),
		HorizontalBarChart("", TimeOp, filter),
		ResultsTable(TimeOp, filter, true),
		HorizontalBarChart("", Speed, filter),
		ResultsTable(Speed, filter, true),
	)

	writeReportAndCompareToExpected(t, []string{job1, job2, job3}, cfg, "compare_n.html")
}

func TestWriteCompareReport(t *testing.T) {
	resetChartId()
	cfg := &ReportConfig{
		Title:   "Comparative report",
		verbose: true,
	}

	filter := ""

	cfg.AddSections(
		JobsTable(),
		HorizontalDeltaChart("", TimeOp, filter),
		ResultsDeltaTable(TimeOp, filter, true),
		HorizontalDeltaChart("", Speed, filter),
		ResultsDeltaTable(Speed, filter, true),
	)

	writeReportAndCompareToExpected(t, []string{job1, job2}, cfg, "compare.html")
}

func TestWriteCustomReports(t *testing.T) {
	resetChartId()

	tests := []struct {
		name string
		jobs []string
	}{
		{name: "custom1", jobs: []string{job1, job2, job3}},
		{name: "custom2", jobs: []string{job1, job2, job3}},
		{name: "custom_labels", jobs: []string{job1, job2}},
		{name: "compare1", jobs: []string{job1, job2}},
		{name: "compare2", jobs: []string{job1, job2}},
	}

	for _, test := range tests {
		specName := test.name + ".json"
		reportName := test.name + ".html"
		t.Run(
			"Custom report: "+specName+" -> "+reportName,
			func(t *testing.T) {
				specPath := filepath.Join("testconfig", specName)

				resetChartId()

				var spec ReportSpec
				err := spec.LoadFile(specPath)
				if err != nil {
					t.Fatal(err)
				}

				cfg := &ReportConfig{}
				err = spec.ConfigureReport(cfg)
				if err != nil {
					t.Fatal(err)
				}

				writeReportAndCompareToExpected(t, test.jobs, cfg, reportName)
			},
		)
	}
}

func writeReportAndCompareToExpected(t *testing.T, jobIds []string, reportConfig *ReportConfig, expectedReportName string) {
	var err error

	c := mockClient{}

	dataTable, err := CreateDataTable(c, jobIds...)
	if err != nil {
		t.Fatal(err)
	}

	if !dataTable.HasSpeed() {
		t.Fatalf("Expected speed data")
	}

	outputFilePath := filepath.Join(t.TempDir(), "report.html")
	file, err := os.Create(outputFilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	err = WriteReport(reportConfig, dataTable, file)
	if err != nil {
		t.Fatal(err)
	}

	assertReportEqual(t, outputFilePath, filepath.Join("testdata", expectedReportName))
}

func assertReportEqual(t *testing.T, reportPath string, expectedReportPath string) {
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}

	expectedReportBytes, err := os.ReadFile(expectedReportPath)
	if err != nil {
		t.Fatal(err)
	}

	normalizedReport := normalizeNumbers(reportBytes)
	normalizedExpected := normalizeNumbers(expectedReportBytes)

	if !bytes.Equal(normalizedReport, normalizedExpected) {
		const overwriteTestData = false
		if overwriteTestData {
			err := os.WriteFile(expectedReportPath, reportBytes, 0644)
			if err != nil {
				t.Log(err)
			}
		}

		// Diagnostics: show the first difference
		reportLines := bytes.Split(normalizedReport, []byte("\n"))
		expectedLines := bytes.Split(normalizedExpected, []byte("\n"))
		for i := 0; i < len(reportLines) && i < len(expectedLines); i++ {
			if !bytes.Equal(reportLines[i], expectedLines[i]) {
				t.Errorf("First mismatch at line %d:\nGot:  %s\nWant: %s", i+1, string(reportLines[i]), string(expectedLines[i]))
				break
			}
		}
		if len(reportLines) != len(expectedLines) {
			t.Errorf("Line count mismatch: Got %d, Want %d", len(reportLines), len(expectedLines))
		}

		t.Fatalf("Report %s does not match expected %s (normalized comparison failed)", reportPath, expectedReportPath)
	}
}

var floatRegex = regexp.MustCompile(`\d+\.\d+([eE][+-]?\d+)?`)

func normalizeNumbers(input []byte) []byte {
	const precision = 10
	return floatRegex.ReplaceAllFunc(input, func(m []byte) []byte {
		v, err := strconv.ParseFloat(string(m), 64)
		if err != nil {
			return m
		}
		p := math.Pow(10, float64(precision))
		rounded := math.Round(v*p) / p
		return []byte(strconv.FormatFloat(rounded, 'f', -1, 64))
	})
}
