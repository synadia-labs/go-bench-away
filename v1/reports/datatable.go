package reports

import (
	"fmt"
	"io"

	"github.com/synadia-labs/go-bench-away/v1/core"
	"golang.org/x/perf/benchstat"
)

type DataTable interface {
	HasSpeed() bool
}

type dataTableImpl struct {
	jobs        []*core.JobRecord
	jobLabels   []string
	collection  benchstat.Collection
	timeOpTable *benchstat.Table
	speedTable  *benchstat.Table
}

func (dt *dataTableImpl) HasSpeed() bool {
	return dt.speedTable != nil
}

func CreateDataTable(client JobRecordClient, jobIds ...string) (DataTable, error) {
	if len(jobIds) == 0 {
		return nil, fmt.Errorf("No jobs provided")
	} else if countUnique(jobIds) != len(jobIds) {
		return nil, fmt.Errorf("The list of job IDs contains duplicates")
	}

	dataTable := dataTableImpl{
		jobs: make([]*core.JobRecord, len(jobIds)),
		collection: benchstat.Collection{
			Alpha:      kDeltaTestAlpha,
			AddGeoMean: false,
			DeltaTest:  benchstat.UTest,
			Order:      nil, // Preserve order
		},
	}

	for i, jobId := range jobIds {
		job, _, err := client.LoadJob(jobId)
		if err != nil {
			return nil, err
		}
		if job.Status != core.Succeeded && job.Status != core.Failed {
			return nil, fmt.Errorf("Job %s status is %v", job.Id, job.Status)
		}

		fmt.Printf("Loading job %s\n", jobId)
		dataTable.jobs[i] = job
		if err := addJobResults(&dataTable.collection, client, jobId, job); err != nil {
			return nil, err
		}
	}

	dataTable.jobLabels = createJobLabels(dataTable.jobs)

	if len(dataTable.collection.Tables()) == 0 {
		return nil, fmt.Errorf("Jobs don't overlap in benchmarks,")
	}

	for _, table := range dataTable.collection.Tables() {
		switch table.Metric {
		case string(TimeOp):
			dataTable.timeOpTable = table
		case string(Speed):
			dataTable.speedTable = table
		default:
			fmt.Printf("Ignoring results metric '%s'\n", table.Metric)
		}
	}

	return &dataTable, nil
}

// addJobResults streams the artifact into benchstat instead of retaining a
// complete copy of the raw benchmark output in memory while it is parsed.
func addJobResults(collection *benchstat.Collection, client JobRecordClient, jobId string, job *core.JobRecord) error {
	reader, writer := io.Pipe()
	artifactErr := make(chan error, 1)
	go func() {
		err := client.LoadResultsArtifact(job, writer)
		_ = writer.CloseWithError(err)
		artifactErr <- err
	}()

	parseErr := collection.AddFile(jobId, reader)
	if parseErr != nil {
		_ = reader.CloseWithError(parseErr)
	} else {
		_ = reader.Close()
	}
	loadErr := <-artifactErr
	if parseErr != nil {
		return parseErr
	}
	return loadErr
}

func (dt *dataTableImpl) mapJobs(f func(*core.JobRecord) string) []string {
	mapped := make([]string, len(dt.jobs))
	for i, job := range dt.jobs {
		mapped[i] = f(job)
	}
	return mapped
}
