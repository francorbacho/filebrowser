# installation

```yaml
services:
  bin:
    image: ghcr.io/francorbacho/filebrowser
    ports:
      - 7667:8000
    volumes:
      - /data/bin:/files
    command: --enable-upload
    environment:
      - TITLE="File Server"
      - EXTRA_HEADERS=""
    restart: unless-stopped
```

# images

![filebrowser's dark theme](https://files.fran.cam/static/filebrowser-dark.png)

![filebrowser's light theme](https://files.fran.cam/static/filebrowser-light.png)

# metrics

- `filebrowser_info` - Build info
- `filebrowser_uptime_seconds` - Uptime
- `filebrowser_http_requests_total{status}` - HTTP requests
- `filebrowser_uploads_total{status}` - Uploads
- `filebrowser_operations_total{type}` - File operations
- `filebrowser_memory_bytes{type}` - Memory usage
- `filebrowser_goroutines` - Goroutines
- `filebrowser_gc_total` - GC count
- `filebrowser_config{setting}` - Config