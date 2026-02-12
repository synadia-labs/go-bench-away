package client

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/synadia-labs/go-bench-away/v1/core"

	"github.com/nats-io/nats.go"
)

func (c *Client) QueueName() string {
	return c.options.jobsQueueName
}

func (c *Client) SubmitJob(params core.JobParameters) (*core.JobRecord, error) {

	job := core.NewJob(params)

	jobRecordKey := fmt.Sprintf(kJobRecordKeyTmpl, job.Id)
	_, err := c.jobsRepository.Create(jobRecordKey, job.Bytes())
	if err != nil {
		return nil, fmt.Errorf("Failed to create job record: %v", err)
	}

	submitMsg := nats.NewMsg(c.options.jobsSubmitSubject)
	submitMsg.Header.Add(kJobIdHeader, job.Id)
	submitMsg.Header.Add(nats.MsgIdHdr, job.Id)

	_, pubErr := c.js.PublishMsg(submitMsg)
	if pubErr != nil {
		return nil, fmt.Errorf("Failed to submit job: %v", pubErr)
	}

	return job, nil
}

func (c *Client) CancelJob(jobId string) error {

	jobRecord, revision, err := c.LoadJob(jobId)
	if err != nil {
		return err
	}

	if jobRecord.Status != core.Submitted {
		return fmt.Errorf("cannot cancel job in state %s", jobRecord.Status.String())
	}

	jobRecord.SetFinalStatus(core.Cancelled)

	_, err = c.UpdateJob(jobRecord, revision)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) LoadRecentJobs(limit, offset int) ([]*core.JobRecord, error) {
	return c.LoadJobs(limit, offset, false)
}

func (c *Client) LoadJobs(limit, offset int, asc bool) ([]*core.JobRecord, error) {
	jobs := []*core.JobRecord{}

	sInfo, err := c.js.StreamInfo(c.options.jobsQueueStreamName)
	if err == nats.ErrMsgNotFound {
		return []*core.JobRecord{}, nil
	} else if err != nil {
		return nil, err
	}

	if sInfo.State.Msgs == 0 {
		return []*core.JobRecord{}, nil
	}

	firstSeq := sInfo.State.FirstSeq
	lastSeq := sInfo.State.LastSeq

	var startSeq, endSeq uint64
	var step int

	if asc {
		startSeq = firstSeq
		endSeq = lastSeq
		step = 1
	} else {
		startSeq = lastSeq
		endSeq = firstSeq
		step = -1
	}

	skipped := 0   // actual messages skipped for offset
	collected := 0 // actual messages collected for limit

	for i := startSeq; ; i += uint64(step) {
		if asc {
			if i > endSeq {
				break
			}
		} else {
			if i < endSeq || i == 0 {
				break
			}
		}

		rawMsg, err := c.js.GetMsg(c.options.jobsQueueStreamName, i)
		if err != nil {
			continue
		}

		jobId := rawMsg.Header.Get(kJobIdHeader)
		if jobId == "" {
			continue
		}

		jobRecordKey := fmt.Sprintf(kJobRecordKeyTmpl, jobId)
		kve, err := c.jobsRepository.Get(jobRecordKey)
		if err != nil {
			continue
		}

		job, err := core.LoadJob(kve.Value())
		if err != nil {
			continue
		}

		if skipped < offset {
			skipped++
			continue
		}

		jobs = append(jobs, job)
		collected++

		if limit > 0 && collected >= limit {
			break
		}
	}

	return jobs, nil
}

func (c *Client) GetQueueStatus() (*core.QueueStatus, error) {
	qs := &core.QueueStatus{}

	sInfo, err := c.js.StreamInfo(c.options.jobsQueueStreamName)
	if err == nats.ErrMsgNotFound {
		return qs, nil
	} else if err != nil {
		return nil, err
	}

	qs.SubmittedCount = sInfo.State.Msgs

	return qs, nil
}

func (c *Client) FindJobOffset(query string) (int, error) {
	if query == "" {
		return -1, nil
	}

	sInfo, err := c.js.StreamInfo(c.options.jobsQueueStreamName)
	if err != nil {
		return -1, nil
	}
	if sInfo.State.Msgs == 0 {
		return -1, nil
	}

	firstSeq := sInfo.State.FirstSeq
	lastSeq := sInfo.State.LastSeq

	seqToOffset := make(map[uint64]int)
	msgIndex := 0
	for i := firstSeq; i <= lastSeq; i++ {
		rawMsg, err := c.js.GetMsg(c.options.jobsQueueStreamName, i)
		if err != nil {
			continue
		}
		jobId := rawMsg.Header.Get(kJobIdHeader)
		if jobId == "" {
			continue
		}
		jobRecordKey := fmt.Sprintf(kJobRecordKeyTmpl, jobId)
		if _, err := c.jobsRepository.Get(jobRecordKey); err != nil {
			continue
		}
		seqToOffset[i] = msgIndex
		msgIndex++
	}

	for i := lastSeq; i >= firstSeq; i-- {
		offset, exists := seqToOffset[i]
		if !exists {
			continue
		}

		rawMsg, err := c.js.GetMsg(c.options.jobsQueueStreamName, i)
		if err != nil {
			continue
		}

		jobId := rawMsg.Header.Get(kJobIdHeader)
		if jobId == "" {
			continue
		}

		jobRecordKey := fmt.Sprintf(kJobRecordKeyTmpl, jobId)
		kve, err := c.jobsRepository.Get(jobRecordKey)
		if err != nil {
			continue
		}

		job, err := core.LoadJob(kve.Value())
		if err != nil {
			continue
		}

		if containsIgnoreCase(job.Id, query) ||
			containsIgnoreCase(job.Parameters.GitRef, query) ||
			containsIgnoreCase(job.Parameters.GitRemote, query) ||
			containsIgnoreCase(job.Parameters.TestsFilterExpr, query) {
			return offset, nil
		}

		if i == 0 {
			break
		}
	}

	return -1, nil
}

func (c *Client) LoadJobsFiltered(limit, offset int, asc bool, statuses []core.JobStatus) ([]*core.JobRecord, int, error) {
	var jobs []*core.JobRecord

	sInfo, err := c.js.StreamInfo(c.options.jobsQueueStreamName)
	if err == nats.ErrMsgNotFound {
		return jobs, 0, nil
	} else if err != nil {
		return nil, 0, err
	}

	if sInfo.State.Msgs == 0 {
		return jobs, 0, nil
	}

	statusSet := make(map[core.JobStatus]bool, len(statuses))
	for _, s := range statuses {
		statusSet[s] = true
	}

	firstSeq := sInfo.State.FirstSeq
	lastSeq := sInfo.State.LastSeq

	var startSeq, endSeq uint64
	var step int
	if asc {
		startSeq = firstSeq
		endSeq = lastSeq
		step = 1
	} else {
		startSeq = lastSeq
		endSeq = firstSeq
		step = -1
	}

	skipped := 0   // filtered messages skipped for offset
	collected := 0 // filtered messages collected for limit
	scanned := 0   // total messages scanned (for scan depth limit)
	fetchLimit := limit + 1
	if limit <= 0 {
		fetchLimit = 0 // no limit
	}

	maxScan := 0
	if len(statusSet) > 0 {
		allTransient := true
		for s := range statusSet {
			if s != core.Submitted && s != core.Running {
				allTransient = false
				break
			}
		}
		if allTransient {
			maxScan = 50
		}
	}

	for i := startSeq; ; i += uint64(step) {
		if asc {
			if i > endSeq {
				break
			}
		} else {
			if i < endSeq || i == 0 {
				break
			}
		}

		if maxScan > 0 && scanned >= maxScan {
			break
		}

		rawMsg, err := c.js.GetMsg(c.options.jobsQueueStreamName, i)
		if err != nil {
			scanned++
			continue
		}
		scanned++

		jobId := rawMsg.Header.Get(kJobIdHeader)
		if jobId == "" {
			continue
		}

		jobRecordKey := fmt.Sprintf(kJobRecordKeyTmpl, jobId)
		kve, err := c.jobsRepository.Get(jobRecordKey)
		if err != nil {
			continue
		}

		job, err := core.LoadJob(kve.Value())
		if err != nil {
			continue
		}

		if len(statusSet) > 0 && !statusSet[job.Status] {
			continue
		}

		if skipped < offset {
			skipped++
			continue
		}

		jobs = append(jobs, job)
		collected++

		if fetchLimit > 0 && collected >= fetchLimit {
			break
		}
	}

	hasMore := limit > 0 && len(jobs) > limit
	if hasMore {
		jobs = jobs[:limit]
	}

	totalFiltered := offset + len(jobs)
	if hasMore {
		totalFiltered++
	}

	return jobs, totalFiltered, nil
}

func (c *Client) CountJobsByStatus() (map[core.JobStatus]int, error) {
	watcher, err := c.jobsRepository.WatchAll()
	if err != nil {
		return nil, fmt.Errorf("failed to watch KV: %v", err)
	}
	defer func() { _ = watcher.Stop() }()

	counts := make(map[core.JobStatus]int)
	for entry := range watcher.Updates() {
		if entry == nil {
			break
		}
		if entry.Operation() != nats.KeyValuePut {
			continue
		}
		job, err := core.LoadJob(entry.Value())
		if err != nil {
			continue
		}
		counts[job.Status]++
	}
	return counts, nil
}

func (c *Client) LoadJobsByKV(limit, offset int, statuses []core.JobStatus) ([]*core.JobRecord, map[core.JobStatus]int, error) {
	watcher, err := c.jobsRepository.WatchAll()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to watch KV: %v", err)
	}
	defer func() { _ = watcher.Stop() }()

	statusSet := make(map[core.JobStatus]bool, len(statuses))
	for _, s := range statuses {
		statusSet[s] = true
	}

	counts := make(map[core.JobStatus]int)
	var matched []*core.JobRecord

	for entry := range watcher.Updates() {
		if entry == nil {
			break
		}
		if entry.Operation() != nats.KeyValuePut {
			continue
		}
		job, err := core.LoadJob(entry.Value())
		if err != nil {
			continue
		}
		counts[job.Status]++
		if len(statusSet) == 0 || statusSet[job.Status] {
			matched = append(matched, job)
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Created.After(matched[j].Created)
	})

	if offset >= len(matched) {
		return nil, counts, nil
	}
	matched = matched[offset:]
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}

	return matched, counts, nil
}

func (c *Client) FailStaleJobs() (int, error) {
	watcher, err := c.jobsRepository.WatchAll()
	if err != nil {
		return 0, fmt.Errorf("failed to watch KV: %v", err)
	}
	defer func() { _ = watcher.Stop() }()

	type staleJob struct {
		id      string
		runtime time.Duration
		timeout time.Duration
	}

	var staleJobs []staleJob
	var skippedActive int
	for entry := range watcher.Updates() {
		if entry == nil {
			break
		}
		if entry.Operation() != nats.KeyValuePut {
			continue
		}
		job, err := core.LoadJob(entry.Value())
		if err != nil {
			continue
		}
		if job.Status != core.Running {
			continue
		}
		runtime := time.Since(job.Started)
		if runtime > job.Parameters.Timeout {
			staleJobs = append(staleJobs, staleJob{
				id:      job.Id,
				runtime: runtime,
				timeout: job.Parameters.Timeout,
			})
		} else {
			skippedActive++
		}
	}

	fmt.Printf("Found %d running jobs exceeding timeout, %d still within timeout\n",
		len(staleJobs), skippedActive)

	updated := 0
	for _, sj := range staleJobs {
		job, revision, err := c.LoadJob(sj.id)
		if err != nil {
			fmt.Printf("  Skip %s: %v\n", sj.id, err)
			continue
		}
		if job.Status != core.Running {
			continue
		}
		fmt.Printf("  Failing %s (ran %s, timeout %s)\n",
			sj.id, sj.runtime.Truncate(time.Minute), sj.timeout)
		job.SetFinalStatus(core.Failed)
		_, err = c.UpdateJob(job, revision)
		if err != nil {
			fmt.Printf("  Failed to update %s: %v\n", sj.id, err)
			continue
		}
		updated++
	}
	return updated, nil
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
