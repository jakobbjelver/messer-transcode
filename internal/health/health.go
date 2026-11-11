// Package health produces one verdict about the whole system, for a human
// reading a terminal and for a shell script that must exit non-zero.
//
// The checks are chosen from what has actually broken here, not from what is
// easy to measure. Every one of them earned its place:
//
//   - a Homebrew library went missing after an upgrade and every Tdarr job
//     failed for months while the health check reported green. Hence the
//     dependency sweep.
//   - a node that had not reported in days looked identical to a node that was
//     mid-encode. Hence "silent while work is queued" as a failure.
//   - four films in the library are zero bytes and nothing said so.
package health

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/internal/version"
)

// Status is the verdict for one check, and for the report as a whole.
type Status string

const (
	OK       Status = "ok"
	Degraded Status = "degraded"
	Broken   Status = "broken"
)

// Role is where the checks are running. The server is in a container with no
// media volume and no encoder, so asking it about either would be a lie.
type Role string

const (
	RoleNode   Role = "node"
	RoleServer Role = "server"
)

// Check is one thing that was verified.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Fix is one sentence an operator can act on, or empty when there is
	// nothing to do.
	Fix string `json:"fix,omitempty"`
}

// Report is the single object both `doctor` and /api/v1/health return.
type Report struct {
	Status     Status            `json:"status"`
	Role       Role              `json:"role"`
	Version    map[string]string `json:"version"`
	Checks     []Check           `json:"checks"`
	LastEncode int64             `json:"last_encode,omitempty"`
	Queued     int               `json:"queued"`
	Now        int64             `json:"now"`
	DurationMS int64             `json:"duration_ms"`
}

// In is everything Collect needs. Store may be nil when the database cannot be
// opened, which is itself a finding.
type In struct {
	Config     *config.Config
	ConfigPath string
	Store      *store.Store
	DBPath     string
	Role       Role

	// OpenError is why the store could not be opened, when that is the case.
	OpenError error

	// ServerReport is the verdict from the live orchestrator, when the caller
	// could reach it. Anything that depends on the database is taken from there
	// rather than from a local read: a host read of a bind-mounted WAL file is
	// stale by minutes while the container is writing it, which turns a healthy
	// system into a false E_NODE_SILENT.
	ServerReport *Report

	// SkipHostChecks disables the checks that only make sense on the machine
	// that runs the encoder: binaries, encoder, disk. Tests set it so a verdict
	// does not depend on what the build machine happens to have installed.
	SkipHostChecks bool

	Now time.Time
}

// Collect runs the checks and folds them into one verdict.
func Collect(ctx context.Context, in In) Report {
	start := time.Now()
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	rep := Report{
		Role: in.Role,
		Version: map[string]string{
			"version":    version.Version,
			"commit":     version.Commit,
			"build_date": version.BuildDate,
			"go":         version.Go(),
		},
		Now: now.Unix(),
	}

	add := func(c Check) { rep.Checks = append(rep.Checks, c) }

	add(checkConfig(in))
	if in.ServerReport == nil {
		// Otherwise the orchestrator's own database check is used below; two
		// checks for one thing is how a report starts contradicting itself.
		add(checkDatabase(ctx, in))
	}

	if in.Role == RoleNode && !in.SkipHostChecks {
		// These two do not read the config, so they are worth running even
		// when it is broken.
		bins := checkBinaries()
		add(bins)
		// If the tools are missing, the encoder check can only report the same
		// thing twice. One root cause, one code.
		if bins.Status == OK {
			add(checkEncoder(ctx))
		}
		if in.Config != nil {
			add(checkDisk(in))
			add(checkQuarantine(in, now))
		}
	}
	// Anything that reads the queue is taken from the live orchestrator when we
	// can reach it: a local read of the bind-mounted database lags behind the
	// container's writes by minutes, which reports a working system as silent.
	if in.ServerReport != nil {
		for _, c := range in.ServerReport.Checks {
			switch c.Name {
			case "database", "node", "window", "progress":
				c.Detail = c.Detail + " (from the orchestrator)"
				add(c)
			}
		}
		rep.Queued = in.ServerReport.Queued
	} else if in.Store != nil && in.Config != nil {
		add(checkNodeSeen(ctx, in, now))
		add(checkWindow(ctx, in, now))
		add(checkProgress(ctx, &rep, in, now))
		rep.Queued = queued(ctx, in.Store)
	}

	rep.Status = fold(rep.Checks)
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep
}

// fold is the only place the words ok / degraded / broken are decided.
func fold(checks []Check) Status {
	status := OK
	for _, c := range checks {
		switch c.Status {
		case Broken:
			return Broken
		case Degraded:
			status = Degraded
		}
	}
	return status
}

// ── checks ────────────────────────────────────────────────────────────────

func checkConfig(in In) Check {
	if in.Config == nil {
		c := Check{Name: "config", Status: Broken}
		if in.OpenError != nil {
			c.Detail = in.OpenError.Error()
		} else {
			c.Detail = "no configuration was loaded"
		}
		// A file that is there but will not parse is a different problem from
		// a path that is wrong, and the fix is different.
		if in.ConfigPath != "" {
			if _, err := os.Stat(in.ConfigPath); err == nil {
				c.Code = "E_CONFIG_INVALID"
				c.Fix = "fix " + in.ConfigPath + ", then run: messer-transcode validate " + in.ConfigPath
				return c
			}
		}
		c.Code = "E_CONFIG_MISSING"
		c.Fix = "check the path passed with --config"
		return c
	}
	// Re-validate from disk: the running process may have started before an
	// edit, and a config that does not parse is the most common cause of a
	// night that did nothing.
	if in.ConfigPath != "" {
		if _, err := config.Load(in.ConfigPath); err != nil {
			return Check{Name: "config", Status: Broken, Code: "E_CONFIG_INVALID",
				Detail: err.Error(), Fix: "fix " + in.ConfigPath + ", then validate it"}
		}
	}
	return Check{Name: "config", Status: OK,
		Detail: fmt.Sprintf("%d libraries, %s", len(in.Config.Libraries), describeSchedule(in.Config.Schedule))}
}

// describeSchedule says when work may run, in the config's own words.
func describeSchedule(s config.Schedule) string {
	if len(s.Windows) == 0 {
		return "no window"
	}
	var parts []string
	for _, w := range s.Windows {
		parts = append(parts, strings.Join(w.Days, ",")+" "+w.Start+"-"+w.End)
	}
	return strings.Join(parts, "; ")
}

func checkDatabase(ctx context.Context, in In) Check {
	if in.Store == nil {
		return Check{Name: "database", Status: Broken, Code: "E_DB_UNAVAILABLE",
			Detail: "the state database could not be opened", Fix: "check paths and permissions for " + in.DBPath}
	}
	db := in.Store.DB()

	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return Check{Name: "database", Status: Broken, Code: "E_DB_UNREADABLE", Detail: err.Error()}
	}
	if integrity != "ok" {
		return Check{Name: "database", Status: Broken, Code: "E_DB_CORRUPT",
			Detail: integrity, Fix: "restore from a backup; do not run encodes against this file"}
	}

	var files int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM files").Scan(&files); err != nil {
		return Check{Name: "database", Status: Broken, Code: "E_DB_SCHEMA", Detail: err.Error(),
			Fix: "the schema is older or newer than this binary"}
	}
	return Check{Name: "database", Status: OK, Detail: fmt.Sprintf("%d files, integrity ok", files)}
}

// checkBinaries looks for the tools and, crucially, for the libraries they need.
//
// The dependency sweep is the check that would have caught the outage that
// started this project: mkvtoolnix kept working until a Homebrew upgrade
// removed a dylib underneath it, and every job failed for months.
func checkBinaries() Check {
	var missing []string
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		path, err := exec.LookPath(name)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		if broken := missingDeps(path); len(broken) > 0 {
			return Check{Name: "binaries", Status: Broken, Code: "E_BINARY_DEP_MISSING",
				Detail: fmt.Sprintf("%s cannot load %s", path, strings.Join(broken, ", ")),
				Fix:    "brew reinstall the package that provides it"}
		}
	}
	if len(missing) > 0 {
		return Check{Name: "binaries", Status: Broken, Code: "E_BINARY_MISSING",
			Detail: "not on PATH: " + strings.Join(missing, ", "),
			Fix:    "install ffmpeg, or fix PATH for the process that runs the node"}
	}
	return Check{Name: "binaries", Status: OK, Detail: "ffmpeg and ffprobe present, dependencies resolve"}
}

// missingDeps reports dynamic libraries the binary references but cannot find.
// System libraries are skipped: they live in the dyld shared cache and have no
// file on disk, so a Stat would report every one of them missing.
func missingDeps(path string) []string {
	out, err := exec.Command("otool", "-L", path).Output()
	if err != nil {
		return nil // not a Mach-O binary, or otool is unavailable: not a finding
	}
	var missing []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		dep := fields[0]
		// The first line is the binary's own header ("/path/to/ffmpeg:") and
		// would otherwise read as a dependency on itself.
		if strings.HasSuffix(dep, ":") {
			continue
		}
		if !strings.HasPrefix(dep, "/") || strings.HasPrefix(dep, "/usr/lib/") ||
			strings.HasPrefix(dep, "/System/") {
			continue
		}
		if _, err := os.Stat(dep); err != nil {
			missing = append(missing, dep)
		}
	}
	return missing
}

// checkEncoder proves the media engine works, rather than assuming it does.
// A one-second synthetic encode is the whole test.
func checkEncoder(ctx context.Context) Check {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=30", "-t", "1",
		"-c:v", "hevc_videotoolbox", "-f", "null", "-")
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return Check{Name: "encoder", Status: Broken, Code: "E_ENCODER_UNAVAILABLE",
			Detail: detail, Fix: "VideoToolbox is only available natively; check this is running on the host, not in a container"}
	}
	return Check{Name: "encoder", Status: OK, Detail: "hevc_videotoolbox encoded a test frame"}
}

func checkDisk(in In) Check {
	need := float64(in.Config.Safety.MinFreeGB)
	var low []string
	for _, dir := range []string{in.Config.Paths.Scratch, in.Config.Paths.Quarantine} {
		free, err := freeGB(dir)
		if err != nil {
			// A missing directory under a mounted volume is a very different
			// problem from a missing volume, and the fix is different.
			fix := "the volume is not mounted"
			if _, parentErr := os.Stat(filepath.Dir(dir)); parentErr == nil {
				fix = "create the directory, or point the config at one that exists"
			}
			return Check{Name: "disk", Status: Broken, Code: "E_PATH_UNWRITABLE",
				Detail: fmt.Sprintf("%s: %v", dir, err), Fix: fix}
		}
		if free < need {
			low = append(low, fmt.Sprintf("%s has %.0f GB (needs %d)", dir, free, in.Config.Safety.MinFreeGB))
		}
	}
	for _, lib := range in.Config.Libraries {
		if _, err := os.Stat(lib.Root); err != nil {
			return Check{Name: "disk", Status: Broken, Code: "E_LIBRARY_MISSING",
				Detail: lib.Root + ": " + err.Error(), Fix: "mount the media volume"}
		}
	}
	if len(low) > 0 {
		return Check{Name: "disk", Status: Broken, Code: "E_DISK_LOW",
			Detail: strings.Join(low, "; "), Fix: "free space, or lower safety.min_free_gb"}
	}
	free, _ := freeGB(in.Config.Paths.Scratch)
	return Check{Name: "disk", Status: OK, Detail: fmt.Sprintf("scratch has %.0f GB free", free)}
}

// checkQuarantine notices when originals stop expiring.
//
// The quarantine grows by the size of every replaced file. If the sweep stops —
// the node is down, the marker is stale, the code regressed — the first sign is
// a directory older than the keep window, long before the disk complains.
func checkQuarantine(in In, now time.Time) Check {
	// Off is a legitimate configuration now: the swap is still one rename, but
	// there is no copy to fall back on. Say that plainly rather than reporting
	// a directory that will never grow.
	if !in.Config.Safety.QuarantineEnabled() {
		return Check{Name: "quarantine", Status: OK,
			Detail: "off — replaced originals are not kept"}
	}
	root := in.Config.Paths.Quarantine
	if root == "" {
		return Check{Name: "quarantine", Status: OK, Detail: "not configured"}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Check{Name: "quarantine", Status: OK, Detail: "nothing quarantined yet"}
	}
	keep := in.Config.Safety.KeepQuarantineDays
	var oldest time.Time
	var files int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d, err := time.ParseInLocation("2006-01-02", e.Name(), now.Location())
		if err != nil {
			continue
		}
		files++
		if oldest.IsZero() || d.Before(oldest) {
			oldest = d
		}
	}
	if files == 0 {
		return Check{Name: "quarantine", Status: OK, Detail: "nothing quarantined yet"}
	}

	age := int(now.Sub(oldest).Hours() / 24)
	if keep > 0 && age > keep+1 {
		return Check{Name: "quarantine", Status: Degraded, Code: "E_SWEEP_STALE",
			Detail: fmt.Sprintf("oldest is %d days old but keep_quarantine_days is %d", age, keep),
			Fix:    "the node sweeps daily; check pm2 logs transcode-node for E_SWEEP"}
	}
	return Check{Name: "quarantine", Status: OK,
		Detail: fmt.Sprintf("%d dated directories, oldest %d days (keep %d)", files, age, keep)}
}

func checkWindow(ctx context.Context, in In, now time.Time) Check {
	if in.Config.Schedule.Timezone != "" {
		if _, err := time.LoadLocation(in.Config.Schedule.Timezone); err != nil {
			return Check{Name: "window", Status: Broken, Code: "E_TIMEZONE",
				Detail: err.Error(), Fix: "use an IANA name such as Europe/Copenhagen"}
		}
	}

	// Who is actually in charge right now? A pause or a forced window is not a
	// fault, but doctor reporting the schedule while an operator holds the queue
	// shut would be a lie — and a pause nobody remembers is how a night is lost.
	if in.Store != nil {
		if ctl, err := in.Store.Control(ctx); err == nil {
			switch {
			case ctl.Paused:
				since := now.Sub(time.Unix(ctl.PausedAt, 0)).Round(time.Minute)
				detail := fmt.Sprintf("the queue is paused, since %s", since)
				code, status := "E_QUEUE_PAUSED", Degraded
				if now.Sub(time.Unix(ctl.PausedAt, 0)) > 12*time.Hour {
					code = "E_QUEUE_PAUSED_LONG"
					detail = fmt.Sprintf("the queue has been paused for %s", since)
				}
				return Check{Name: "window", Status: status, Code: code, Detail: detail,
					Fix: "resume it: Queue → Resume, or `messer-transcode queue resume`"}
			case ctl.Override == store.OverrideOpen:
				detail := "window forced open by hand"
				if ctl.Until > 0 {
					detail += " until " + time.Unix(ctl.Until, 0).Format("15:04")
				}
				return Check{Name: "window", Status: OK, Detail: detail}
			case ctl.Override == store.OverrideClose:
				return Check{Name: "window", Status: OK, Code: "E_WINDOW_FORCED_SHUT",
					Detail: "window forced shut by hand; no new claims"}
			}
		}
	}

	return Check{Name: "window", Status: OK,
		Detail: fmt.Sprintf("%s, drain %dm", describeSchedule(in.Config.Schedule), in.Config.Schedule.DrainBeforeMinutes)}
}

// checkNodeSeen is the check that would have caught the Tdarr collapse: work
// queued, a node that had not reported in days, and nothing complaining.
func checkNodeSeen(ctx context.Context, in In, now time.Time) Check {
	q := queued(ctx, in.Store)
	if q == 0 {
		return Check{Name: "node", Status: OK, Detail: "nothing queued, so a quiet node is expected"}
	}

	var last sql.NullInt64
	err := in.Store.DB().QueryRowContext(ctx, `
SELECT MAX(at) FROM events WHERE code IN ('METRICS','E_NODE_INTERRUPTED') OR level='info'`).Scan(&last)
	if err != nil || !last.Valid {
		return Check{Name: "node", Status: Broken, Code: "E_NODE_SILENT",
			Detail: fmt.Sprintf("%d files queued and no node has ever reported", q),
			Fix:    "start the node: pm2 start transcode-node"}
	}
	silent := now.Sub(time.Unix(last.Int64, 0))
	if silent > 30*time.Minute {
		return Check{Name: "node", Status: Broken, Code: "E_NODE_SILENT",
			Detail: fmt.Sprintf("%d files queued, last activity %s ago", q, silent.Round(time.Minute)),
			Fix:    "pm2 status transcode-node; check its log for E_NODE_SERVER"}
	}
	return Check{Name: "node", Status: OK,
		Detail: fmt.Sprintf("last activity %s ago", silent.Round(time.Minute))}
}

// checkProgress answers the question the old health check never asked: is this
// thing actually finishing films?
func checkProgress(ctx context.Context, rep *Report, in In, now time.Time) Check {
	var last sql.NullInt64
	err := in.Store.DB().QueryRowContext(ctx, `SELECT MAX(finished_at) FROM files WHERE state='done'`).Scan(&last)
	if err != nil {
		return Check{Name: "progress", Status: Degraded, Detail: err.Error()}
	}
	if last.Valid {
		rep.LastEncode = last.Int64
	}

	q := queued(ctx, in.Store)
	if q == 0 {
		return Check{Name: "progress", Status: OK, Detail: "queue empty"}
	}
	if !last.Valid {
		return Check{Name: "progress", Status: Degraded, Code: "E_NO_PROGRESS",
			Detail: fmt.Sprintf("%d files queued and nothing has finished yet", q),
			Fix:    "expected on a fresh install; otherwise check the node log"}
	}
	age := now.Sub(time.Unix(last.Int64, 0))
	if age > 7*24*time.Hour {
		return Check{Name: "progress", Status: Degraded, Code: "E_STALE_PROGRESS",
			Detail: fmt.Sprintf("last finished encode was %s ago", age.Round(time.Hour)),
			Fix:    "check whether the node is running and whether jobs are failing"}
	}
	return Check{Name: "progress", Status: OK,
		Detail: fmt.Sprintf("last finished encode %s ago", age.Round(time.Minute))}
}

// ── helpers ───────────────────────────────────────────────────────────────

func queued(ctx context.Context, st *store.Store) int {
	var n int
	_ = st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM files WHERE state IN ('planned','encoding','verifying','replacing') AND decision IN ('encode','remux')`).Scan(&n)
	return n
}

func freeGB(path string) (float64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() {
		path = filepath.Dir(path)
	}
	return freeGBPath(path)
}
