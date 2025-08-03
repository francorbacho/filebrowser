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
    restart: unless-stopped
```
