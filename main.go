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
	filesDir = "/files"
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
	// Build information - set via ldflags during build
	GitCommit = "unknown"
	BuildDate = "unknown"
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

	data := struct {
		CurrentPath  string
		ParentURL    string
		Files        []FileInfo
		Title        string
		ExtraHeaders string
		GitCommit    string
		BuildDate    string
	}{
		CurrentPath:  urlPath,
		ParentURL:    parentURL,
		Files:        fileInfos,
		Title:        title,
		ExtraHeaders: extraHeaders,
		GitCommit:    GitCommit,
		BuildDate:    BuildDate,
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
	finalPath := filepath.Join(fullPath, filename)

	absFilesDir, _ := filepath.Abs(filesDir)
	absFinalPath, _ := filepath.Abs(finalPath)
	if !strings.HasPrefix(absFinalPath, absFilesDir) {
		http.Error(w, "Invalid file path", http.StatusForbidden)
		return
	}

	dst, err := os.Create(finalPath)
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
  }
  header {
    background: var(--header-bg);
    padding: 10px;
    border-bottom: 1px solid var(--border-color);
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  main { padding: 10px; overflow: auto; height: calc(100vh - 130px); }
  table { width: 100%; border-collapse: collapse; }
  th {
    text-align: left;
    padding: 8px 4px;
    border-bottom: 1px solid var(--border-color);
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
  button {
    padding: 4px 8px;
    margin-left: 5px;
    background: var(--header-bg);
    color: var(--text-color);
    border: 1px solid var(--border-color);
    cursor: pointer;
  }
  button:hover { opacity: 0.8; }
  a { color: var(--text-color); }
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
    <h1>{{.CurrentPath}}</h1>
    <input type="text" id="search" class="search-box" placeholder="Filter by filename..." autocomplete="off">
    <form class="upload-form" action="/upload" method="post" enctype="multipart/form-data">
      <input type="hidden" name="dir" value="{{.CurrentPath}}">
      <input type="file" name="file" id="file-input" required>
      <label for="file-input" class="file-input-label" id="file-label">Choose file...</label>
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

  <footer>
    Build: {{.GitCommit}} | {{.BuildDate}}
    <button class="theme-toggle" onclick="toggleTheme()" title="Toggle theme">🌓</button>
  </footer>

  <script>
    function toggleTheme() {
      const html = document.documentElement;
      const current = html.getAttribute('data-theme');
      const next = current === 'dark' ? 'light' : 'dark';
      html.setAttribute('data-theme', next);
    }

    if (window.matchMedia('(prefers-color-scheme: dark)').matches) {
      document.documentElement.setAttribute('data-theme', 'dark');
    }

    document.getElementById('file-input').addEventListener('change', function(e) {
      const label = document.getElementById('file-label');
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
  </script>
</body>
</html>`
