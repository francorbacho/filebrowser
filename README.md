# installation

```yaml
services:
  bin:
    image: ghcr.io/francorbacho/filebrowser
    ports:
      - 7667:8000
    volumes:
      - /data/bin:/files
    environment:
      - EXTRA_HEADERS=""
      - TITLE=""
      - DISABLE_UPLOAD="false"
    restart: unless-stopped
```

# images

![filebrowser's dark theme](https://files.fran.cam/static/filebrowser-dark.png)

![filebrowser's light theme](https://files.fran.cam/static/filebrowser-light.png)