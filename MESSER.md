# MESSER.md — Messer Transcode

Port: **1 (React + shadcn)** · Brand: **`brand-transcode`** (neutral ink `oklch(.30 .008 260)`,
Phosphor `FilmSlate` mark) · Sink: **`/ui`** · Canon: **2.1.1**

Adopted 2026-11-11 (rev 10 of `docs/PLAN.md`). The server-rendered "film slate"
(`internal/ui/`: 4 templates, `slate.css`, `logo.svg`) is **deleted** — no redirects, no legacy
routes. The SPA owns `/`; the Go binary embeds `web/app/dist` and serves `index.html` for every
non-`/api` path.

Tokens were installed with `scripts/install.mjs --brand transcode --dir src/styles/messer`; the
shell and blocks live in `web/app/src/components/ui/`; atoms are `shadcn add` output, patched to the
canon (below). `AppShell`'s `linkComponent` is `src/components/router-link.tsx` (react-router's
`Link` takes `to`, the block takes `href`), and `ThemeToggle` is driven by
`src/hooks/use-theme.ts` + `src/components/theme-control.tsx` — the block is presentational.

## Screens

| Route | What | API |
|---|---|---|
| `/` | reclaim claim, counts, gate strip, now, 5 runs, 14 events | `/api/v1/status`, `/api/v1/runs?last=5`, `/api/v1/events?limit=14`, `/api/v1/stream` |
| `/queue` | filter tabs with counts, 100-row pages, prev/next | `/api/v1/files` |
| `/files/:id` | decision, why, source/target, VMAF vs tier, tracks, last error | `/api/v1/files/{id}` |
| `/runs` | 30 runs + the schedule they were cut against | `/api/v1/runs?last=30` |
| `/settings` | build, `doctor` checks, redacted config | `/api/v1/config`, `/api/v1/health` |

## Local decisions

- Copy voice (`the reclaim`, `Left alone`, "Nothing encoding") and the mono section eyebrows.
- The 60-tick gate strip is app composition (`src/components/tick-strip.tsx`), not a registry block.
- **No elapsed-time figure on the live card.** `store.Live.Since` is the last heartbeat, not the job's
  start, so an "elapsed" number would be invented. Show progress, speed and bytes written instead.

## Atom patches (why they are not stock shadcn)

| Atom | Change | Rule |
|---|---|---|
| `button` | sizes 28/32/36 (`h-7`/`h-8`/`h-9`), `sm`=28 `lg`=36; no `shadow-xs` on outline; focus left to the base `:focus-visible` rule | R6 density, R2 no shadows, §11 focus |
| `badge` | `rounded-none`; ring utilities dropped | R1 square, §11 focus |
| `card` | `CardTitle` is mono 16/500 | R3 mono never above h3 |
| `table` | `TableHead` is mono 10.5/upper/`.1em` in `--text-3` | R5 meta labels |
| `progress` | `rounded-none` | R1 square |
| `tabs` | trigger ring utilities dropped, base focus rule applies | §11 focus |

`transition-all` was replaced with explicit `transition-[background-color,border-color,color,box-shadow]`
on the button: `transition-colors` animates `outline-color`, so a focus ring starts at `currentColor`.

## Deviations

| Date | Rule | What | Exit |
|---|---|---|---|
| 2026-11-11 | R1 | `badge.tsx` and `dev/sink.tsx` contain `rounded-full` for the avatar and status dot fixtures; the static scan cannot see the element. | Permanent |

## Notes from adoption

- **`shadcn add jakobbjelver/messer-ui/<item>` does not work with shadcn 4.3.0.** Both the
  `owner/repo/item` form and a `@messer` namespaced registry pointed at
  `https://raw.githubusercontent.com/jakobbjelver/messer-ui/main/registry/{name}/registry-item.json`
  resolve the item and then print "No files updated": registry items carry a repo-relative
  `files[].path`, not inline `content`, so the CLI cannot fetch the file. Blocks were therefore copied
  from `registry/<item>/<item>.tsx` and are gated by `blocks.mjs --app .` (11/11 byte-identical).
  Fixing this is a messer-ui task: publish the `/r/` tree the index expects.
- Dev ports: **Vite 5299, Go 8299.** Never 8099 — that is the container's published port *and* the
  node's configured target (`server.url: http://127.0.0.1:8099`), so a local server there answers the
  live node's registration and silently takes the encoder out of service. It happened for one window
  on 2026-11-11 (`E_NODE_SILENT` after 8h18m). `vite.config.ts` proxies `/api` to
  `VITE_API_ORIGIN`, default `http://127.0.0.1:8299`.
- The SPA's assets are hashed and cached `immutable`; `index.html` is `no-cache`. A missing `dist`
  answers `503 frontend not built: run make web` and leaves the node API serving — the UI may not take
  the orchestrator down.

## Verify

```bash
make web-test                      # vitest: formatters, state map, schedule copy
make web                           # vite build → web/app/dist (embedded)
make test                          # go test ./... (needs the dist above)
node ~/Repos/messer-ui/scripts/check.mjs --app . --static
node ~/Repos/messer-ui/scripts/blocks.mjs --app .
node ~/Repos/messer-ui/scripts/brand-check.mjs --app .
# full pass, needs the sink served:
cd web/app && VITE_API_ORIGIN=http://127.0.0.1:8299 npx vite --port 5299
node ~/Repos/messer-ui/scripts/check.mjs --url http://localhost:5299/ui --app .
```
