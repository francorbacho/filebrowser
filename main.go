package main

import (
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	port     = ":8000"
	filesDir = "/files"
)

type FileInfo struct {
	Name         string
	Size         string
	LastModified string
	IsDir        bool
	URL          string
}

// Breadcrumb segment for the current path
type Crumb struct {
	Label string
	URL   string
}

var (
	title         = getEnv("TITLE", "File Server")
	extraHeaders  = getEnv("EXTRA_HEADERS", "")
	enableUpload  = getBoolEnv("ENABLE_UPLOAD", false)
	enableMetrics = getBoolEnv("ENABLE_METRICS", false)
	// Build information - set via ldflags during build
	GitCommit = "unknown"
	BuildDate = "unknown"
	// Metrics
	startTime           = time.Now()
	httpRequestsTotal   atomic.Uint64
	httpRequestsSuccess atomic.Uint64
	httpRequestsError   atomic.Uint64
	uploadsTotal        atomic.Uint64
	uploadsSuccess      atomic.Uint64
	uploadsError        atomic.Uint64
	directoryLists      atomic.Uint64
	fileServes          atomic.Uint64

	requestDurationBuckets = map[string]map[string]*atomic.Uint64{
		"GET": {
			"0.1":  &atomic.Uint64{},
			"0.5":  &atomic.Uint64{},
			"1.0":  &atomic.Uint64{},
			"+Inf": &atomic.Uint64{},
		},
		"POST": {
			"0.1":  &atomic.Uint64{},
			"0.5":  &atomic.Uint64{},
			"1.0":  &atomic.Uint64{},
			"+Inf": &atomic.Uint64{},
		},
	}
	requestDurationSum   = map[string]*atomic.Uint64{
		"GET":  &atomic.Uint64{},
		"POST": &atomic.Uint64{},
	}
	requestDurationCount = map[string]*atomic.Uint64{
		"GET":  &atomic.Uint64{},
		"POST": &atomic.Uint64{},
	}
)

func main() {
	var enableUploadFlag bool
	var enableMetricsFlag bool
	flag.BoolVar(&enableUploadFlag, "enable-upload", false, "Enable file uploads")
	flag.BoolVar(&enableMetricsFlag, "enable-metrics", false, "Enable metrics endpoint")
	flag.Parse()

	if enableUploadFlag {
		enableUpload = true
	}

	if enableMetricsFlag {
		enableMetrics = true
	}

	os.MkdirAll(filesDir, os.ModePerm)

	http.HandleFunc("/", pathHandler)
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/metrics", metricsHandler)

	log.Printf("Server running at http://localhost%s", port)

	if enableUpload {
		log.Printf("File uploads are enabled")
	} else {
		log.Printf("File uploads are disabled")
	}

	if enableMetrics {
		log.Printf("Metrics endpoint available at /metrics")
	} else {
		log.Printf("Metrics endpoint is disabled")
	}

	log.Fatal(http.ListenAndServe(port, nil))
}

func pathHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		if r.URL.Path != "/metrics" {
			duration := time.Since(start).Seconds()
			recordRequestDuration(r.Method, duration)
		}
	}()

	if r.URL.Path != "/metrics" {
		httpRequestsTotal.Add(1)
	}

	urlPath := r.URL.Path
	fullPath := filepath.Join(filesDir, urlPath)

	absFilesDir, _ := filepath.Abs(filesDir)
	absPath, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absPath, absFilesDir) {
		if r.URL.Path != "/metrics" {
			httpRequestsError.Add(1)
		}
		http.NotFound(w, r)
		return
	}

	if _, err := os.Stat(filesDir); os.IsNotExist(err) {
		if r.URL.Path != "/metrics" {
			httpRequestsError.Add(1)
		}
		http.Error(w, fmt.Sprintf("files dir %s: no such directory", filesDir), http.StatusInternalServerError)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if r.URL.Path != "/metrics" {
			httpRequestsError.Add(1)
		}
		if urlPath == "/" {
			http.Error(w, fmt.Sprintf("files dir %s: inaccessible or bad perms", filesDir), http.StatusInternalServerError)
		} else {
			http.Error(w, fmt.Sprintf("%s: no such file or directory", fullPath), http.StatusNotFound)
		}
		return
	}

	if info.IsDir() {
		if !strings.HasSuffix(urlPath, "/") {
			http.Redirect(w, r, urlPath+"/", http.StatusFound)
			return
		}
		directoryLists.Add(1)
		listDirectory(w, fullPath, urlPath)
	} else {
		fileServes.Add(1)
		http.ServeFile(w, r, fullPath)
	}

	if r.URL.Path != "/metrics" {
		httpRequestsSuccess.Add(1)
	}
}

func listDirectory(w http.ResponseWriter, dirPath string, urlPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		http.Error(w, "Error reading directory", http.StatusInternalServerError)
		return
	}

	parentURL := "/"
	if urlPath != "/" {
		parts := strings.Split(strings.Trim(urlPath, "/"), "/")
		if len(parts) > 1 {
			parentURL = "/" + strings.Join(parts[:len(parts)-1], "/") + "/"
		} else if len(parts) == 1 {
			parentURL = "/"
		}
	}

	fileInfos := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		entryURL := entry.Name()
		var size string
		if entry.IsDir() {
			entryURL += "/"
			subDirPath := filepath.Join(dirPath, entry.Name())
			if subEntries, err := os.ReadDir(subDirPath); err == nil {
				itemCount := len(subEntries)
				if itemCount == 1 {
					size = "1 item"
				} else {
					size = fmt.Sprintf("%d items", itemCount)
				}
			} else {
				size = "-"
			}
		} else {
			size = formatSize(info.Size())
		}

		fileInfos = append(fileInfos, FileInfo{
			Name:         entry.Name(),
			Size:         size,
			LastModified: info.ModTime().Format("2006-01-02 15:04-07:00"),
			IsDir:        entry.IsDir(),
			URL:          entryURL,
		})
	}

	sort.Slice(fileInfos, func(i, j int) bool {
		if fileInfos[i].IsDir && !fileInfos[j].IsDir {
			return true
		}
		if !fileInfos[i].IsDir && fileInfos[j].IsDir {
			return false
		}
		return fileInfos[i].Name < fileInfos[j].Name
	})

	tmpl := template.New("index").Funcs(template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	})

	tmpl, err = tmpl.Parse(htmlTemplate)
	if err != nil {
		http.Error(w, "Error rendering page", http.StatusInternalServerError)
		return
	}

	breadcrumbs := buildBreadcrumbs(urlPath)

	data := struct {
		CurrentPath   string
		ParentURL     string
		Files         []FileInfo
		Title         string
		ExtraHeaders  string
		GitCommit     string
		BuildDate     string
		DisableUpload bool
		Breadcrumbs   []Crumb
	}{
		CurrentPath:   urlPath,
		ParentURL:     parentURL,
		Files:         fileInfos,
		Title:         title,
		ExtraHeaders:  extraHeaders,
		GitCommit:     GitCommit,
		BuildDate:     BuildDate,
		DisableUpload: !enableUpload,
		Breadcrumbs:   breadcrumbs,
	}

	tmpl.Execute(w, data)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		recordRequestDuration(r.Method, duration)
	}()

	uploadsTotal.Add(1)

	if !enableUpload {
		uploadsError.Add(1)
		http.Error(w, "File uploads are disabled", http.StatusForbidden)
		return
	}

	if r.Method != "POST" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	targetDir := r.FormValue("dir")
	if targetDir == "" {
		targetDir = "/"
	}

	fullPath := filepath.Join(filesDir, filepath.Clean(targetDir))
	os.MkdirAll(fullPath, os.ModePerm)

	file, header, err := r.FormFile("file")
	if err != nil {
		uploadsError.Add(1)
		http.Error(w, "Invalid upload", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := strings.ReplaceAll(header.Filename, "/", "_")
	finalPath := filepath.Join(fullPath, filename)

	absFilesDir, _ := filepath.Abs(filesDir)
	absFinalPath, _ := filepath.Abs(finalPath)
	if !strings.HasPrefix(absFinalPath, absFilesDir) {
		uploadsError.Add(1)
		http.Error(w, "Invalid file path", http.StatusForbidden)
		return
	}

	dst, err := os.Create(finalPath)
	if err != nil {
		uploadsError.Add(1)
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
	if err != nil {
		uploadsError.Add(1)
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}

	uploadsSuccess.Add(1)
	http.Redirect(w, r, targetDir, http.StatusSeeOther)
}

func recordRequestDuration(method string, duration float64) {
	buckets, exists := requestDurationBuckets[method]
	if !exists {
		return
	}

	requestDurationCount[method].Add(1)
	durationMicros := uint64(duration * 1_000_000)
	requestDurationSum[method].Add(durationMicros)

	bucketThresholds := []string{"0.1", "0.5", "1.0", "+Inf"}
	thresholds := []float64{0.1, 0.5, 1.0, math.Inf(1)}

	for i, threshold := range thresholds {
		if duration <= threshold {
			buckets[bucketThresholds[i]].Add(1)
		}
	}
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	if !enableMetrics {
		http.Error(w, "Metrics are disabled", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptime := time.Since(startTime).Seconds()

	fmt.Fprintf(w, "# HELP filebrowser_info Information about the file browser\n")
	fmt.Fprintf(w, "# TYPE filebrowser_info gauge\n")
	fmt.Fprintf(w, "filebrowser_info{version=\"%s\",build_date=\"%s\"} 1\n", GitCommit, BuildDate)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_uptime_seconds Total uptime in seconds\n")
	fmt.Fprintf(w, "# TYPE filebrowser_uptime_seconds gauge\n")
	fmt.Fprintf(w, "filebrowser_uptime_seconds %.2f\n", uptime)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_http_requests_total Total number of HTTP requests\n")
	fmt.Fprintf(w, "# TYPE filebrowser_http_requests_total counter\n")
	fmt.Fprintf(w, "filebrowser_http_requests_total{status=\"total\"} %d\n", httpRequestsTotal.Load())
	fmt.Fprintf(w, "filebrowser_http_requests_total{status=\"success\"} %d\n", httpRequestsSuccess.Load())
	fmt.Fprintf(w, "filebrowser_http_requests_total{status=\"error\"} %d\n", httpRequestsError.Load())
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_uploads_total Total number of file uploads\n")
	fmt.Fprintf(w, "# TYPE filebrowser_uploads_total counter\n")
	fmt.Fprintf(w, "filebrowser_uploads_total{status=\"total\"} %d\n", uploadsTotal.Load())
	fmt.Fprintf(w, "filebrowser_uploads_total{status=\"success\"} %d\n", uploadsSuccess.Load())
	fmt.Fprintf(w, "filebrowser_uploads_total{status=\"error\"} %d\n", uploadsError.Load())
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_operations_total Total number of file operations\n")
	fmt.Fprintf(w, "# TYPE filebrowser_operations_total counter\n")
	fmt.Fprintf(w, "filebrowser_operations_total{type=\"directory_list\"} %d\n", directoryLists.Load())
	fmt.Fprintf(w, "filebrowser_operations_total{type=\"file_serve\"} %d\n", fileServes.Load())
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_http_request_duration_seconds HTTP request duration in seconds\n")
	fmt.Fprintf(w, "# TYPE filebrowser_http_request_duration_seconds histogram\n")

	for method, buckets := range requestDurationBuckets {
		for bucket, count := range buckets {
			fmt.Fprintf(w, "filebrowser_http_request_duration_seconds_bucket{le=\"%s\",method=\"%s\"} %d\n", bucket, method, count.Load())
		}

		sumMicros := requestDurationSum[method].Load()
		sumSeconds := float64(sumMicros) / 1_000_000
		fmt.Fprintf(w, "filebrowser_http_request_duration_seconds_sum{method=\"%s\"} %.6f\n", method, sumSeconds)
		fmt.Fprintf(w, "filebrowser_http_request_duration_seconds_count{method=\"%s\"} %d\n", method, requestDurationCount[method].Load())
	}
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_memory_bytes Memory usage in bytes\n")
	fmt.Fprintf(w, "# TYPE filebrowser_memory_bytes gauge\n")
	fmt.Fprintf(w, "filebrowser_memory_bytes{type=\"alloc\"} %d\n", m.Alloc)
	fmt.Fprintf(w, "filebrowser_memory_bytes{type=\"sys\"} %d\n", m.Sys)
	fmt.Fprintf(w, "filebrowser_memory_bytes{type=\"heap_alloc\"} %d\n", m.HeapAlloc)
	fmt.Fprintf(w, "filebrowser_memory_bytes{type=\"heap_sys\"} %d\n", m.HeapSys)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_goroutines Current number of goroutines\n")
	fmt.Fprintf(w, "# TYPE filebrowser_goroutines gauge\n")
	fmt.Fprintf(w, "filebrowser_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_gc_total Total number of garbage collections\n")
	fmt.Fprintf(w, "# TYPE filebrowser_gc_total counter\n")
	fmt.Fprintf(w, "filebrowser_gc_total %d\n", m.NumGC)
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "# HELP filebrowser_config Configuration settings\n")
	fmt.Fprintf(w, "# TYPE filebrowser_config gauge\n")
	if enableUpload {
		fmt.Fprintf(w, "filebrowser_config{setting=\"uploads_enabled\"} 1\n")
	} else {
		fmt.Fprintf(w, "filebrowser_config{setting=\"uploads_disabled\"} 1\n")
	}
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getBoolEnv(key string, defaultValue bool) bool {
	if value, exists := os.LookupEnv(key); exists {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func buildBreadcrumbs(urlPath string) []Crumb {
	crumbs := []Crumb{{Label: "/", URL: "/"}}
	clean := strings.Trim(urlPath, "/")
	if clean == "" {
		return crumbs
	}
	parts := strings.Split(clean, "/")
	current := "/"
	for _, p := range parts {
		current += p + "/"
		crumbs = append(crumbs, Crumb{Label: p + "/", URL: current})
	}
	return crumbs
}

const htmlTemplate = `<!DOCTYPE html>
<html>
<head>
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><text y='14' font-size='14'>📁</text></svg>">
{{.ExtraHeaders | safeHTML}}
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }

  :root {
    --bg-color: #fff;
    --text-color: #000;
    --header-bg: #f0f0f0;
    --border-color: #ddd;
    --border-light: #eee;
    --row-even: #f8f8f8;
    --footer-bg: #f0f0f0;
    --footer-text: #666;
    --footer-height: 30px;
    --table-margin: 10px;
  }

  @media (prefers-color-scheme: dark) {
    :root {
      --bg-color: #1a1a1a;
      --text-color: #e0e0e0;
      --header-bg: #2a2a2a;
      --border-color: #444;
      --border-light: #333;
      --row-even: #252525;
      --footer-bg: #2a2a2a;
      --footer-text: #888;
    }
  }

  [data-theme="dark"] {
    --bg-color: #1a1a1a;
    --text-color: #e0e0e0;
    --header-bg: #2a2a2a;
    --border-color: #444;
    --border-light: #333;
    --row-even: #252525;
    --footer-bg: #2a2a2a;
    --footer-text: #888;
  }

  [data-theme="light"] {
    --bg-color: #fff;
    --text-color: #000;
    --header-bg: #f0f0f0;
    --border-color: #ddd;
    --border-light: #eee;
    --row-even: #f8f8f8;
    --footer-bg: #f0f0f0;
    --footer-text: #666;
  }

  body {
    font-family: monospace;
    font-size: 14px;
    background: var(--bg-color);
    color: var(--text-color);
    height: 100vh;
    display: flex;
    flex-direction: column;
  }
  header {
    background: var(--header-bg);
    padding: 10px;
    border-bottom: 1px solid var(--border-color);
    display: flex;
    flex-direction: column;
    gap: 10px;
    flex-shrink: 0;
  }
  main {
    flex: 1;
    overflow: auto;
    padding-bottom: var(--footer-height);
  }
  table {
    width: calc(100% - calc(2 * var(--table-margin)));
    border-collapse: collapse;
    margin: var(--table-margin);
  }
  th {
    text-align: left;
    padding: 8px 4px;
    border-bottom: 1px solid var(--border-color);
    position: sticky;
    top: 0;
    background: var(--bg-color);
    z-index: 10;
  }
  td {
    padding: 8px 4px;
    border-bottom: 1px solid var(--border-light);
    white-space: nowrap;
  }
  tr:nth-child(even) { background: var(--row-even); }
  .name { width: 60%; overflow: hidden; text-overflow: ellipsis; }
  .size { width: 15%; }
  .date { width: 25%; }
  .upload-form { display: flex; align-items: center; }
  .search-box {
    padding: 4px;
    width: 100%;
    font-family: monospace;
    background: var(--bg-color);
    color: var(--text-color);
    border: 1px solid var(--border-color);
  }
  input[type="file"] {
    display: none;
  }
  .file-input-label {
    flex-grow: 1;
    padding: 4px 8px;
    background: var(--header-bg);
    color: var(--text-color);
    border: 1px solid var(--border-color);
    cursor: pointer;
    font-family: monospace;
    font-size: 14px;
    text-align: left;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .file-input-label:hover { opacity: 0.8; }
  .file-input-label:disabled,
  .file-input-label.disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .file-input-label.disabled::after {
    content: " 🚫";
    color: #999;
  }
  .drag-disabled {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    background: var(--header-bg);
    border: 2px solid var(--border-color);
    padding: 10px 20px;
    border-radius: 4px;
    font-size: 16px;
    z-index: 1000;
    display: none;
  }
  button {
    padding: 4px 8px;
    margin-left: 5px;
    background: var(--header-bg);
    color: var(--text-color);
    border: 1px solid var(--border-color);
    cursor: pointer;
  }
  button:hover { opacity: 0.8; }
  button:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  a { color: var(--text-color); }
  header h1 a { text-decoration: none; }
  header h1 a:hover { text-decoration: underline; }
  footer {
    position: fixed;
    bottom: 0;
    left: 0;
    right: 0;
    background: var(--footer-bg);
    padding: 5px 40px 5px 10px;
    border-top: 1px solid var(--border-color);
    font-size: 11px;
    color: var(--footer-text);
  }
  .theme-toggle {
    position: absolute;
    right: 5px;
    top: 50%;
    transform: translateY(-50%);
    padding: 4px 8px;
    background: var(--header-bg);
    border: 1px solid var(--border-color);
    color: var(--text-color);
    cursor: pointer;
    font-size: 11px;
    margin: 0;
  }
  .theme-toggle:hover { opacity: 0.8; }
</style>
</head>
<body>
  <header>
    <h1>{{range .Breadcrumbs}}<a href="{{.URL}}">{{.Label}}</a>{{end}}</h1>
    <input type="text" id="search" class="search-box" placeholder="Filter by filename..." autocomplete="off">
    <form class="upload-form" action="/upload" method="post" enctype="multipart/form-data">
      <input type="hidden" name="dir" value="{{.CurrentPath}}">
      <input type="file" name="file" id="file-input" required {{if .DisableUpload}}disabled{{end}}>
      <label for="file-input" class="file-input-label{{if .DisableUpload}} disabled{{end}}" id="file-label">
        {{if .DisableUpload}}Uploads disabled{{else}}Choose file...{{end}}
      </label>
      <button type="submit" {{if .DisableUpload}}disabled{{end}}>Upload</button>
    </form>
  </header>

  <main>
    <table id="file-table">
      <thead>
        <tr>
          <th class="name">Name</th>
          <th class="size">Size</th>
          <th class="date">Last Modified</th>
        </tr>
      </thead>
      <tbody>
        {{if ne .CurrentPath "/"}}
        <tr class="filerow">
          <td class="name">📁 <a href="{{.ParentURL}}">..</a></td>
          <td class="size">-</td>
          <td class="date">-</td>
        </tr>
        {{end}}
        {{range .Files}}
        <tr class="filerow">
          <td class="name">
            {{if .IsDir}}📁{{else}}📄{{end}}
            <a href="{{.URL}}">{{.Name}}{{if .IsDir}}/{{end}}</a>
          </td>
          <td class="size">{{.Size}}</td>
          <td class="date">{{.LastModified}}</td>
        </tr>
        {{end}}
      </tbody>
    </table>
  </main>

  <footer>
    Build: {{.GitCommit}} | {{.BuildDate}}
    <button class="theme-toggle" onclick="toggleTheme()" title="Toggle theme">🌓</button>
  </footer>

  <div id="drag-message" class="drag-disabled"></div>

  <script>
    function toggleTheme() {
      const html = document.documentElement;
      const current = html.getAttribute('data-theme');
      const next = current === 'dark' ? 'light' : 'dark';
      html.setAttribute('data-theme', next);
    }

    document.getElementById('file-input').addEventListener('change', function(e) {
      const label = document.getElementById('file-label');
      if (e.target.disabled) return;
      const fileName = e.target.files[0]?.name || 'Choose file...';
      label.textContent = fileName;
    });

    document.getElementById('search').addEventListener('input', function(e) {
      const term = e.target.value.toLowerCase();
      const rows = document.querySelectorAll('.filerow');

      rows.forEach(row => {
        const link = row.querySelector('.name a');
        if (!link) return;

        const name = link.textContent.toLowerCase();
        if (link.textContent === '..') return;
        row.style.display = name.includes(term) ? '' : 'none';
      });
    });

    // Drag and drop functionality
    const fileInput = document.getElementById('file-input');
    const dragMessage = document.getElementById('drag-message');

    function showDragMessage(text) {
      dragMessage.className = 'drag-disabled';
      dragMessage.textContent = text;
      dragMessage.style.display = 'block';
    }

    function hideDragMessage() {
      dragMessage.style.display = 'none';
    }

    document.addEventListener('dragover', function(e) {
      e.preventDefault();
      const text = fileInput.disabled ? '🚫 Uploads disabled' : '📁 Drop to upload';
      showDragMessage(text);
    });

    document.addEventListener('dragleave', function(e) {
      if (!e.relatedTarget) hideDragMessage();
    });

    document.addEventListener('drop', function(e) {
      e.preventDefault();
      hideDragMessage();
      if (!fileInput.disabled && e.dataTransfer.files.length > 0) {
        fileInput.files = e.dataTransfer.files;
        const label = document.getElementById('file-label');
        label.textContent = e.dataTransfer.files[0].name;
      }
    });
  </script>
</body>
</html>`
