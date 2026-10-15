---
"proxemby": minor
---

Add traffic statistics per route and kind (API, media, images). Run `proxemby stats` for today's, this month's and since-start traffic, or read `/stats` (JSON) and `/metrics` (Prometheus) from the admin listener on `127.0.0.1:9180`. Set `[stats] file` to keep daily totals across restarts.
