package web

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/synadia-labs/go-bench-away/v1/core"
	"github.com/synadia-labs/go-bench-away/v1/reports"
)

//go:embed html/index.html.tmpl
var indexTmpl string

//go:embed html/queue.html.tmpl
var queueTmpl string

var jobResourceRegexp = regexp.MustCompile(
	`^/job/([[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}` +
		`-[[:xdigit:]]{12})/(log|script|results|record|plot|cancel)/?$`,
)

type handler struct {
	client        WebClient
	indexTemplate *template.Template
	queueTemplate *template.Template
}

func NewHandler(c WebClient) http.Handler {
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict expects even number of arguments")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
	}
	return &handler{
		client:        c,
		indexTemplate: template.Must(template.New("index").Parse(indexTmpl)),
		queueTemplate: template.Must(template.New("queue").Funcs(funcMap).Parse(queueTmpl)),
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Reject anything that is not a GET
	if r.Method != http.MethodGet {
		http.Error(w, fmt.Sprintf("Invalid request method: %s", r.Method), http.StatusMethodNotAllowed)
		return
	}

	url := r.URL
	path := url.Path

	fmt.Printf(" > %s %s\n", r.Method, path)

	var err error
	if path == "" || path == "/" {
		err = h.serveIndex(w)
	} else if path == "/queue" || path == "/queue/" {
		err = h.serveQueue(w, r)
	} else if strings.HasPrefix(path, "/job/") {
		groupMatches := jobResourceRegexp.FindStringSubmatch(path)
		if groupMatches == nil || len(groupMatches) != 3 {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		jobId, resource := groupMatches[1], groupMatches[2]

		err = h.serveJobResource(w, jobId, resource)
	} else {
		http.Error(w, "Bad request", http.StatusBadRequest)
	}

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		http.Error(w, fmt.Sprintf("Internal error: %v", err), http.StatusInternalServerError)
	} else {
		fmt.Printf("Ok\n")
	}
}

func (h *handler) serveIndex(w http.ResponseWriter) error {

	qs, err := h.client.GetQueueStatus()
	if err != nil {
		return err
	}

	return h.indexTemplate.Execute(w, qs)
}

func (h *handler) serveQueue(w http.ResponseWriter, r *http.Request) error {

	limit := 10
	offset := 0

	offsetStr := r.URL.Query().Get("offset")
	if offsetStr != "" {
		if val, err := strconv.Atoi(offsetStr); err == nil {
			offset = val
		}
	}

	limitStr := r.URL.Query().Get("limit")
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil {
			limit = val
		}
	}

	// Handle Global Search
	searchQuery := strings.TrimSpace(r.URL.Query().Get("search"))
	if searchQuery != "" {
		foundOffset, err := h.client.FindJobOffset(searchQuery)
		if err != nil {
			return err
		}
		if foundOffset >= 0 {
			// Calculate page start for this offset
			// Page number = (offset / limit) + 1
			// New Offset = (Page number - 1) * limit
			// Actually simpler:
			// newOffset = (foundOffset / limit) * limit
			if limit <= 0 {
				limit = 10
			}
			newOffset := (foundOffset / limit) * limit

			// Redirect to the page containing the item
			// Preserve highlight
			redirectUrl := fmt.Sprintf("/queue?offset=%d&limit=%d&highlight=%s",
				newOffset, limit, searchQuery) // using 'highlight' not 'search' to avoid loop
			http.Redirect(w, r, redirectUrl, http.StatusFound)
			return nil
		}
		// Not found? Just show page 1 (default fallthrough) or maybe show error?
		// For now, fallthrough but maybe we can pass a "flash" error.
		// Let's just fallthrough to page 1 which is standard behavior if filter not matched.
	}

	jobRecords, err := h.client.LoadJobs(limit, offset, true)
	if err != nil {
		return err
	}

	qs, err := h.client.GetQueueStatus()
	if err != nil {
		return err
	}

	// Pagination logic
	if limit <= 0 {
		limit = 10
	}
	totalPages := int((qs.SubmittedCount + uint64(limit) - 1) / uint64(limit))
	currentPage := (offset / limit) + 1
	paginationTokens := calculatePagination(currentPage, totalPages, 2)

	tv := struct {
		QueueName        string
		Jobs             []*core.JobRecord
		Offset           int
		Limit            int
		TotalCount       int
		CurrentPage      int
		TotalPages       int
		PaginationTokens []interface{}
	}{
		QueueName:        h.client.QueueName(),
		Jobs:             jobRecords,
		Offset:           offset,
		Limit:            limit,
		TotalCount:       int(qs.SubmittedCount),
		CurrentPage:      currentPage,
		TotalPages:       totalPages,
		PaginationTokens: paginationTokens,
	}
	return h.queueTemplate.Execute(w, tv)
}

func calculatePagination(current, total, window int) []interface{} {
	var tokens []interface{}

	// Always include first page
	tokens = append(tokens, 1)

	// Range start
	start := current - window
	if start > 2 {
		tokens = append(tokens, "...")
	} else {
		// connect to 1
		start = 2
	}

	// Range end
	end := current + window
	if end < total-1 {
		// Room for ...
	} else {
		end = total - 1
	}

	for i := start; i <= end; i++ {
		if i > 1 && i < total {
			tokens = append(tokens, i)
		}
	}

	if end < total-1 {
		tokens = append(tokens, "...")
	}

	// Always include last page if > 1
	if total > 1 {
		tokens = append(tokens, total)
	}

	return tokens
}

func (h *handler) serveJobResource(w http.ResponseWriter, jobId, resourceType string) error {

	jobRecord, _, err := h.client.LoadJob(jobId)
	if err != nil {
		return fmt.Errorf("Failed to load job '%s': %v", jobId, err)
	}

	switch resourceType {
	case "log":
		err = h.client.LoadLogArtifact(jobRecord, w)
	case "script":
		err = h.client.LoadScriptArtifact(jobRecord, w)
	case "results":
		err = h.client.LoadResultsArtifact(jobRecord, w)
	case "record":
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		err = e.Encode(jobRecord)
	case "plot":
		err = h.serveJobResultsPlot(jobId, w)
	case "cancel":
		err = h.client.CancelJob(jobId)
		if err == nil {
			fmt.Fprintf(w, "Job %s cancelled", jobId)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to load '%s': %v", resourceType, err)
	}

	return nil
}

func (h *handler) serveJobResultsPlot(jobId string, w http.ResponseWriter) error {

	dataTable, err := reports.CreateDataTable(h.client, jobId)
	if err != nil {
		return err
	}

	cfg := reports.ReportConfig{
		Title: fmt.Sprintf("Results report for job %s", jobId),
	}

	cfg.AddSections(
		reports.JobsTable(),
	)

	if dataTable.HasSpeed() {
		cfg.AddSections(
			reports.HorizontalBoxChart("", reports.Speed, ""),
			reports.ResultsTable(reports.Speed, "", true),
		)
	}

	cfg.AddSections(
		reports.HorizontalBoxChart("", reports.TimeOp, ""),
		reports.ResultsTable(reports.TimeOp, "", true),
	)
	// Re-checking Step 36 content for serveJobResultsPlot.
	// Lines 152-157: Speed
	// Lines 159-162: TimeOp.
	// I will correct the second block to TimeOp.

	err = reports.WriteReport(&cfg, dataTable, w)
	if err != nil {
		return err
	}
	return nil
}
