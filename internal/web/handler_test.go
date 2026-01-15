package web

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/synadia-labs/go-bench-away/v1/core"
)

type mockWebClient struct {
	CapturedLimit      int
	CapturedOffset     int
	ReturnLoadJobsErr  error
	ReturnQueueStatus  *core.QueueStatus
	ReturnQueueStatErr error
	ReturnJobs         []*core.JobRecord
}

func (m *mockWebClient) LoadJob(jobId string) (*core.JobRecord, uint64, error) { return nil, 0, nil }
func (m *mockWebClient) GetQueueStatus() (*core.QueueStatus, error) {
	return m.ReturnQueueStatus, m.ReturnQueueStatErr
}
func (m *mockWebClient) LoadRecentJobs(limit, offset int) ([]*core.JobRecord, error) {
	m.CapturedLimit = limit
	m.CapturedOffset = offset
	return m.ReturnJobs, m.ReturnLoadJobsErr
}
func (m *mockWebClient) LoadResultsArtifact(job *core.JobRecord, w io.Writer) error { return nil }
func (m *mockWebClient) LoadLogArtifact(job *core.JobRecord, w io.Writer) error     { return nil }
func (m *mockWebClient) LoadScriptArtifact(job *core.JobRecord, w io.Writer) error  { return nil }
func (m *mockWebClient) CancelJob(id string) error                                  { return nil }
func (m *mockWebClient) QueueName() string                                          { return "test-queue" }
func (m *mockWebClient) FindJobOffset(query string) (int, error)                    { return -1, nil }
func (m *mockWebClient) LoadJobs(limit, offset int, asc bool) ([]*core.JobRecord, error) {
	m.CapturedLimit = limit
	m.CapturedOffset = offset
	return m.ReturnJobs, m.ReturnLoadJobsErr
}

func TestServeQueuePagination(t *testing.T) {
	tests := []struct {
		name             string
		queryParams      map[string]string
		mockJobs         []*core.JobRecord
		mockTotalObj     *core.QueueStatus
		mockLoadErr      error
		mockStatusErr    error
		expectedStatus   int
		expectedLimit    int
		expectedOffset   int
		expectedSubstr   []string // strings expected in the response body
		unexpectedSubstr []string // strings NOT expected in response
	}{
		{
			name:           "Default values",
			queryParams:    map[string]string{},
			mockTotalObj:   &core.QueueStatus{SubmittedCount: 100},
			expectedStatus: http.StatusOK,
			expectedLimit:  10,
			expectedOffset: 0,
			expectedSubstr: []string{
				">Next</a>",
			},
			unexpectedSubstr: []string{
				">Previous</a>", // First page shouldn't have Previous link
			},
		},
		{
			name:           "Page 2 (Middle)",
			queryParams:    map[string]string{"limit": "10", "offset": "10"},
			mockTotalObj:   &core.QueueStatus{SubmittedCount: 100},
			expectedStatus: http.StatusOK,
			expectedLimit:  10,
			expectedOffset: 10,
			expectedSubstr: []string{
				">Previous</a>",
				">Next</a>",
				"offset=0",  // Previous link
				"offset=20", // Next link
			},
		},
		{
			name:           "Last Page",
			queryParams:    map[string]string{"limit": "10", "offset": "90"},
			mockTotalObj:   &core.QueueStatus{SubmittedCount: 100},
			expectedStatus: http.StatusOK,
			expectedLimit:  10,
			expectedOffset: 90,
			expectedSubstr: []string{
				">Previous</a>",
				"offset=80",
			},
			unexpectedSubstr: []string{
				">Next</a>", // Last page shouldn't have Next link
			},
		},
		{
			name:           "LoadRecentJobs Error",
			queryParams:    map[string]string{},
			mockLoadErr:    errors.New("db error"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "GetQueueStatus Error",
			queryParams:    map[string]string{},
			mockJobs:       []*core.JobRecord{},
			mockStatusErr:  errors.New("nats error"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "Custom limit parsing",
			queryParams:    map[string]string{"limit": "20"},
			mockTotalObj:   &core.QueueStatus{SubmittedCount: 100},
			expectedStatus: http.StatusOK,
			expectedLimit:  20,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockWebClient{
				ReturnJobs:         tc.mockJobs,
				ReturnQueueStatus:  tc.mockTotalObj,
				ReturnLoadJobsErr:  tc.mockLoadErr,
				ReturnQueueStatErr: tc.mockStatusErr,
			}
			h := NewHandler(mock)

			u, _ := url.Parse("/queue")
			q := u.Query()
			for k, v := range tc.queryParams {
				q.Set(k, v)
			}
			u.RawQuery = q.Encode()

			req := httptest.NewRequest("GET", u.String(), nil)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Result().StatusCode != tc.expectedStatus {
				t.Errorf("Expected status %d, got %d", tc.expectedStatus, w.Result().StatusCode)
			}

			if tc.expectedStatus == http.StatusOK {
				if mock.CapturedLimit != tc.expectedLimit {
					t.Errorf("Expected limit %d, got %d", tc.expectedLimit, mock.CapturedLimit)
				}
				if mock.CapturedOffset != tc.expectedOffset {
					t.Errorf("Expected offset %d, got %d", tc.expectedOffset, mock.CapturedOffset)
				}
			}

			body := w.Body.String()
			for _, s := range tc.expectedSubstr {
				if !strings.Contains(body, s) {
					t.Errorf("Response body missing expected string: %q", s)
				}
			}
			for _, s := range tc.unexpectedSubstr {
				if strings.Contains(body, s) {
					t.Errorf("Response body contained unexpected string: %q", s)
				}
			}
		})
	}
}

func TestJobResourcesRegexp(t *testing.T) {
	var expectNoMatchCases = []string{
		"",
		"/",
		"/job",
		"/job//log",
		"/job/_2fb41f25-7e17-4383-9e08-8ab115152db2/log",    // Non-hexadecimal char in JobId UUID
		"/job/2fb41f2-7e17-4383-9e08-8ab115152db2/log",      // Short 1 char in 1st group
		"/job/2fb41f25-717-4383-9e08-8ab115152db2/log",      // Short 1 char in middle group
		"/job/2fb41f25-7e17-483-9e08-8ab115152db2/log",      // Short 1 char in middle group
		"/job/2fb41f25-7e17-4383-908-8ab115152db2/log",      // Short 1 char in middle group
		"/job/2fb41f25-7e17-4383-9e08-ab115152db2/log",      // Short 1 char in last group
		"/job/2fb41f257e1743839e088ab115152db2/log",         // Missing dashes
		"/job/2fb41f25-7e17-4383-9e08-8ab115152db2/blah",    // Invalid resource type
		"/job/2fb41f25-7e17-4383-9e08-8ab115152db2/log/foo", // Extra path component
	}

	for _, s := range expectNoMatchCases {
		if jobResourceRegexp.MatchString(s) {
			t.Errorf("Should not have matched, but did: '%s'", s)
		}
	}

	var expectMatchCases = []struct {
		input            string
		expectedJobId    string
		expectedResource string
	}{
		{
			input:            "/job/2fb41f25-7e17-4383-9e08-8ab115152db2/log",
			expectedJobId:    "2fb41f25-7e17-4383-9e08-8ab115152db2",
			expectedResource: "log",
		},
		{
			input:            "/job/2fb41f25-7e17-4383-9e08-8ab115152db2/log/",
			expectedJobId:    "2fb41f25-7e17-4383-9e08-8ab115152db2",
			expectedResource: "log",
		},
		{
			input:            "/job/2fb41f25-7e17-4383-9e08-8ab115152db2/script/",
			expectedJobId:    "2fb41f25-7e17-4383-9e08-8ab115152db2",
			expectedResource: "script",
		},
	}

	for _, tc := range expectMatchCases {
		if !jobResourceRegexp.MatchString(tc.input) {
			t.Errorf("Should have matched, but didn't: '%s'", tc.input)
		}
	}
}

func TestCalculatePagination(t *testing.T) {
	tests := []struct {
		desc     string
		current  int
		total    int
		window   int
		expected []interface{}
	}{
		{
			desc:     "Single page",
			current:  1,
			total:    1,
			window:   2,
			expected: []interface{}{1},
		},
		{
			desc:     "Few pages (no gaps)",
			current:  3,
			total:    5,
			window:   2,
			expected: []interface{}{1, 2, 3, 4, 5},
		},
		{
			desc:     "Gap at end",
			current:  1,
			total:    10,
			window:   2,
			expected: []interface{}{1, 2, 3, "...", 10},
		},
		{
			desc:     "Gap at start",
			current:  10,
			total:    10,
			window:   2,
			expected: []interface{}{1, "...", 8, 9, 10},
		},
		{
			desc:     "Gaps on both sides",
			current:  10,
			total:    20,
			window:   2,
			expected: []interface{}{1, "...", 8, 9, 10, 11, 12, "...", 20},
		},
		{
			desc:     "Window overlap with start",
			current:  4,
			total:    20,
			window:   2,
			expected: []interface{}{1, 2, 3, 4, 5, 6, "...", 20},
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := calculatePagination(tc.current, tc.total, tc.window)
			if len(got) != len(tc.expected) {
				t.Errorf("Expected length %d, got %d", len(tc.expected), len(got))
				return
			}
			for i, v := range got {
				if v != tc.expected[i] {
					t.Errorf("Index %d: expected %v, got %v", i, tc.expected[i], v)
				}
			}
		})
	}
}
