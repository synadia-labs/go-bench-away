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

var jobResourceRegexp = regexp.MustCompile(`^/job/([[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12})/(log|script|results|record|plot|cancel)/?$`) //nolint:lll

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

	statusParam := r.URL.Query().Get("status")
	if statusParam == "" {
		statusParam = "submitted,running"
	}
	var statuses []core.JobStatus
	if statusParam != "all" {
		statuses = parseStatusFilter(statusParam)
	}

	searchQuery := strings.TrimSpace(r.URL.Query().Get("search"))
	if searchQuery != "" {
		foundOffset, err := h.client.FindJobOffset(searchQuery)
		if err != nil {
			return err
		}
		if foundOffset >= 0 {
			if limit <= 0 {
				limit = 10
			}
			newOffset := (foundOffset / limit) * limit
			redirectUrl := fmt.Sprintf("/queue?offset=%d&limit=%d&highlight=%s&status=%s",
				newOffset, limit, searchQuery, statusParam)
			http.Redirect(w, r, redirectUrl, http.StatusFound)
			return nil
		}
	}

	jobRecords, statusCounts, err := h.client.LoadJobsByKV(limit, offset, statuses)
	if err != nil {
		return err
	}

	totalFiltered := 0
	if statusParam == "all" || len(statuses) == 0 {
		for _, count := range statusCounts {
			totalFiltered += count
		}
	} else {
		for _, s := range statuses {
			totalFiltered += statusCounts[s]
		}
	}

	if limit <= 0 {
		limit = 10
	}
	totalPages := (totalFiltered + limit - 1) / limit
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
		StatusFilter     string
	}{
		QueueName:        h.client.QueueName(),
		Jobs:             jobRecords,
		Offset:           offset,
		Limit:            limit,
		TotalCount:       totalFiltered,
		CurrentPage:      currentPage,
		TotalPages:       totalPages,
		PaginationTokens: paginationTokens,
		StatusFilter:     statusParam,
	}
	return h.queueTemplate.Execute(w, tv)
}

func parseStatusFilter(param string) []core.JobStatus {
	statusMap := map[string]core.JobStatus{
		"submitted": core.Submitted,
		"running":   core.Running,
		"failed":    core.Failed,
		"succeeded": core.Succeeded,
		"cancelled": core.Cancelled,
	}
	var statuses []core.JobStatus
	for _, s := range strings.Split(param, ",") {
		s = strings.TrimSpace(strings.ToLower(s))
		if status, ok := statusMap[s]; ok {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

func calculatePagination(current, total, window int) []interface{} {
	var tokens []interface{}

	tokens = append(tokens, 1)

	start := current - window
	if start > 2 {
		tokens = append(tokens, "...")
	} else {
		start = 2
	}

	end := current + window
	if end < total-1 {
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
	err = reports.WriteReport(&cfg, dataTable, w)
	if err != nil {
		return err
	}
	return nil
}
