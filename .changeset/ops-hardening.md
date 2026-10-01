---
"proxemby": minor
---

Make proxemby easier to operate:

- Add `--version` and log the version at startup.
- Add `response_header_timeout` (default 60 seconds): requests to an upstream that sends no response headers in time get `504` instead of hanging. Streaming bodies are not limited.
- Shut down gracefully on `SIGTERM` and `SIGINT`, giving open requests up to 10 seconds to finish.
- Upgrading the Debian package now reloads systemd and restarts a running service.
