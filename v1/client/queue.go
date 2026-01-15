package client

import (
	"fmt"
	"strings"

	"github.com/synadia-labs/go-bench-away/v1/core"

	"github.com/nats-io/nats.go"
)

func (c *Client) QueueName() string {
	return c.options.jobsQueueName
}

func (c *Client) SubmitJob(params core.JobParameters) (*core.JobRecord, error) {

	// Create a job object from parameters
	job := core.NewJob(params)

	// Create a record in jobs repository
	jobRecordKey := fmt.Sprintf(kJobRecordKeyTmpl, job.Id)
	_, err := c.jobsRepository.Create(jobRecordKey, job.Bytes())
	if err != nil {
		return nil, fmt.Errorf("Failed to create job record: %v", err)
	}

	// Submit job in the queue
	submitMsg := nats.NewMsg(c.options.jobsSubmitSubject)
	// Message is empty, header points to job record in repository
	submitMsg.Header.Add(kJobIdHeader, job.Id)
	// For deduplication
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

	// Get stream info to know bounds
	sInfo, err := c.js.StreamInfo(c.options.jobsQueueStreamName)
	if err == nats.ErrMsgNotFound {
		return []*core.JobRecord{}, nil
	} else if err != nil {
		return nil, err
	}

	if sInfo.State.Msgs == 0 {
		return []*core.JobRecord{}, nil
	}

	var startSeq uint64
	var endSeq uint64
	var step int

	if asc {
		// Ascending (FIFO): Start from FirstSeq + offset
		startSeq = sInfo.State.FirstSeq + uint64(offset)
		endSeq = sInfo.State.LastSeq
		step = 1
		if startSeq > endSeq {
			return []*core.JobRecord{}, nil
		}
	} else {
		// Descending (LIFO/Recent): Start from LastSeq - offset
		startSeq = sInfo.State.LastSeq
		if uint64(offset) < sInfo.State.Msgs {
			// Careful with gap logic.
			// If we assume no gaps, Seq count = Msgs count.
			// But if gaps, LastSeq - FirstSeq + 1 != Msgs.
			// The original code used Sequence subtraction:
			// startSeq = lastSubmitMsg.Sequence - offset.
			// This works if gaps are ignored or fatal.
			if uint64(offset) > startSeq {
				return []*core.JobRecord{}, nil
			}
			startSeq = startSeq - uint64(offset)
		} else {
			// Offset >= Total Msgs (approx).
			// Let's rely on simple sequence math as before.
			if uint64(offset) > startSeq {
				return []*core.JobRecord{}, nil
			}
			startSeq = startSeq - uint64(offset)
		}
		endSeq = sInfo.State.FirstSeq
		if endSeq == 0 {
			endSeq = 1
		} // Sequences start at 1 usually
		step = -1
	}

	for i := startSeq; ; i += uint64(step) {
		// Boundary checks
		if asc {
			if i > endSeq {
				break
			}
		} else {
			if i < endSeq || i == 0 {
				break
			} // i starts high, goes down. Stop if < FirstSeq.
		}

		// Limit check
		// Count how many we've processed?
		// Actually limit applies to *results found*.
		if limit > 0 && len(jobs) >= limit {
			break
		}

		rawMsg, err := c.js.GetMsg(c.options.jobsQueueStreamName, i)
		if err != nil {
			// If message is missing (gap), just continue?
			// Original code ERROR'd on missing msg.
			// But usually safely skipping gaps is better for robustness.
			// Let's try to match original robustness but maybe slightly safer?
			// "Failed retrieve submit request"
			// If I want to be safe for "ascending", I should probably skip gaps.
			// But let's error to be consistent with original behavior if preferred.
			// Actually, for Ascending, if I start at 1 and it was purged, I fail.
			// So skipping is better.
			// Let's Skip if err (assuming not found/purged).
			continue
		}

		jobId := rawMsg.Header.Get(kJobIdHeader)
		if jobId == "" {
			continue
		}

		jobRecordKey := fmt.Sprintf(kJobRecordKeyTmpl, jobId)

		kve, err := c.jobsRepository.Get(jobRecordKey)
		if err != nil {
			// Skip if repository record missing
			continue
			// Original: returned error "Failed to job %s record"
		}

		job, err := core.LoadJob(kve.Value())
		if err != nil {
			continue
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

func (c *Client) GetQueueStatus() (*core.QueueStatus, error) {
	qs := &core.QueueStatus{}

	lastSubmitMsg, err := c.js.GetLastMsg(c.options.jobsQueueStreamName, c.options.jobsSubmitSubject)
	if err == nats.ErrMsgNotFound {
		return qs, nil
	} else if err != nil {
		return nil, err
	}

	lastSeq := lastSubmitMsg.Sequence
	qs.SubmittedCount = lastSeq

	return qs, nil
}

func (c *Client) FindJobOffset(query string) (int, error) {
	if query == "" {
		return -1, nil
	}

	// For FIFO/Ascending, we need distance from FirstSeq.
	sInfo, err := c.js.StreamInfo(c.options.jobsQueueStreamName)
	if err != nil {
		return -1, nil
	}
	if sInfo.State.Msgs == 0 {
		return -1, nil
	}

	lastSeq := sInfo.State.LastSeq
	firstSeq := sInfo.State.FirstSeq

	// Scan backwards (start from newest, usually what people want)
	// But calculate offset from start.
	for i := lastSeq; i >= firstSeq; i-- {
		rawMsg, err := c.js.GetMsg(c.options.jobsQueueStreamName, i)
		if err != nil {
			// Skip missing messages
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

		// Search Query Check
		if containsIgnoreCase(job.Id, query) ||
			containsIgnoreCase(job.Parameters.GitRef, query) ||
			containsIgnoreCase(job.Parameters.GitRemote, query) ||
			containsIgnoreCase(job.Parameters.TestsFilterExpr, query) {

			// Found it.
			// Offset for FIFO (Ascending) = i - firstSeq.
			// This assumes we are paging by sequence count from start.
			// Re-verify logic:
			// LoadJobs(asc=true) -> startSeq = sInfo.State.FirstSeq + uint64(offset)
			// So if we found item at `i`, then `i = FirstSeq + offset` => `offset = i - FirstSeq`.
			return int(i - firstSeq), nil
		}

		if i == 0 {
			break
		} // Safety breakdown
	}

	return -1, nil
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
