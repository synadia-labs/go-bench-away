package web

import (
	"io"

	"github.com/synadia-labs/go-bench-away/v1/core"
)

type WebClient interface {
	LoadJob(jobId string) (*core.JobRecord, uint64, error)
	GetQueueStatus() (*core.QueueStatus, error)
	FindJobOffset(query string) (int, error)
	LoadRecentJobs(limit, offset int) ([]*core.JobRecord, error)
	LoadJobs(limit, offset int, asc bool) ([]*core.JobRecord, error)
	LoadJobsFiltered(limit, offset int, asc bool, statuses []core.JobStatus) ([]*core.JobRecord, int, error)
	LoadResultsArtifact(job *core.JobRecord, w io.Writer) error
	LoadLogArtifact(job *core.JobRecord, w io.Writer) error
	LoadScriptArtifact(job *core.JobRecord, w io.Writer) error
	CancelJob(id string) error
	CountJobsByStatus() (map[core.JobStatus]int, error)
	LoadJobsByKV(limit, offset int, statuses []core.JobStatus) ([]*core.JobRecord, map[core.JobStatus]int, error)
	QueueName() string
}
