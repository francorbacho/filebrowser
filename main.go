package main

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	port     = ":8000"
	filesDir = "./files"
)

type FileInfo struct {
	Name         string
	Size         string
	LastModified string
	IsDir        bool
	URL          string
}

var (
	title        = getEnv("TITLE", "File Server")
	extraHeaders = getEnv("EXTRA_HEADERS", "")
)

func main() {
	os.MkdirAll(filesDir, os.ModePerm)

	http.HandleFunc("/", pathHandler)
	http.HandleFunc("/upload", uploadHandler)

	log.Printf("Server running at http://localhost%s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

func pathHandler(w http.ResponseWriter, r *http.Request) {
	urlPath := r.URL.Path
	fullPath := filepath.Join(filesDir, urlPath)

	absFilesDir, _ := filepath.Abs(filesDir)
	absPath, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absPath, absFilesDir) {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if info.IsDir() {
		if !strings.HasSuffix(urlPath, "/") {
			http.Redirect(w, r, urlPath+"/", http.StatusFound)
			return
		}
		listDirectory(w, fullPath, urlPath)
	} else {
		http.ServeFile(w, r, fullPath)
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
		if entry.IsDir() {
			entryURL += "/"
		}

		fileInfos = append(fileInfos, FileInfo{
			Name:         entry.Name(),
			Size:         formatSize(info.Size()),
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

	tmpl := template.Must(template.New("index").Parse(htmlTemplate))
	
	tmpl.Funcs(template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
	})
	
	tmpl, err = tmpl.Parse(htmlTemplate)
	if err != nil {
		http.Error(w, "Error rendering page", http.StatusInternalServerError)
		return
	}

	data := struct {
		CurrentPath  string
		ParentURL    string
		Files        []FileInfo
		Title        string
		ExtraHeaders string
	}{
		CurrentPath:  urlPath,
		ParentURL:    parentURL,
		Files:        fileInfos,
		Title:        title,
		ExtraHeaders: extraHeaders,
	}

	tmpl.Execute(w, data)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "Invalid upload", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filename := strings.ReplaceAll(header.Filename, "/", "_")
	dst, err := os.Create(filepath.Join(fullPath, filename))
	if err != nil {
		http.Error(w, "Unable to save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, targetDir, http.StatusSeeOther)
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

const htmlTemplate = `<!DOCTYPE html>
<html>
<head>
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
{{.ExtraHeaders | safeHTML}}
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: monospace; font-size: 14px; background: #fff; color: #000; height: 100vh; }
  header { background: #f0f0f0; padding: 10px; border-bottom: 1px solid #ddd; display: flex; flex-direction: column; }
  main { padding: 10px; overflow: auto; height: calc(100vh - 100px); }
  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; padding: 8px 4px; border-bottom: 1px solid #ddd; }
  td { padding: 8px 4px; border-bottom: 1px solid #eee; white-space: nowrap; }
  tr:nth-child(even) { background: #f8f8f8; }
  .name { width: 60%; overflow: hidden; text-overflow: ellipsis; }
  .size { width: 15%; }
  .date { width: 25%; }
  .upload-form { display: flex; margin-top: 10px; }
  .search-box { margin-bottom: 10px; padding: 4px; width: 100%; font-family: monospace; }
  input[type="file"] { flex-grow: 1; }
  button { padding: 4px 8px; margin-left: 5px; }
</style>
</head>
<body>
  <header>
    <h1>{{.CurrentPath}}</h1>
    <input type="text" id="search" class="search-box" placeholder="Filter by filename..." autocomplete="off">
    <form class="upload-form" action="/upload" method="post" enctype="multipart/form-data">
      <input type="hidden" name="dir" value="{{.CurrentPath}}">
      <input type="file" name="file" required>
      <button type="submit">Upload</button>
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

  <script>
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
  </script>
</body>
</html>`
