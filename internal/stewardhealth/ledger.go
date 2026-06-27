package stewardhealth

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultBucket = time.Hour

type Finding struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Summary  string `json:"summary,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type Snapshot struct {
	Timestamp          time.Time      `json:"timestamp"`
	FindingsTotal      int            `json:"findings_total"`
	FindingsBySeverity map[string]int `json:"findings_by_severity,omitempty"`
	FindingsByKind     map[string]int `json:"findings_by_kind,omitempty"`
	RecoveryBlocked    int            `json:"recovery_blocked,omitempty"`
	QueuedReady        int            `json:"queued_ready,omitempty"`
	ActiveSessions     int            `json:"active_sessions,omitempty"`
	Polecats           map[string]int `json:"polecats,omitempty"`
	DirtyCoreRepos     []string       `json:"dirty_core_repos,omitempty"`
	Findings           []Finding      `json:"findings,omitempty"`
}

type Entry struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	BucketStart time.Time `json:"bucket_start"`
	Fingerprint string    `json:"fingerprint"`
	Snapshot    Snapshot  `json:"snapshot"`
}

type Report struct {
	Path               string         `json:"path"`
	Since              time.Time      `json:"since"`
	Entries            int            `json:"entries"`
	FirstAt            time.Time      `json:"first_at,omitempty"`
	LastAt             time.Time      `json:"last_at,omitempty"`
	FindingsBySeverity map[string]int `json:"findings_by_severity"`
	FindingsByKind     map[string]int `json:"findings_by_kind"`
	RecoveryDebt       Trend          `json:"recovery_debt"`
	FindingsTotal      Trend          `json:"findings_total"`
	Recent             []Entry        `json:"recent,omitempty"`
}

type Trend struct {
	First int `json:"first"`
	Last  int `json:"last"`
	Delta int `json:"delta"`
	Min   int `json:"min"`
	Max   int `json:"max"`
}

func LedgerPath(townRoot string) string {
	return filepath.Join(townRoot, ".beads", "steward-health-ledger.jsonl")
}

func RuntimePath(townRoot string) string {
	return filepath.Join(townRoot, ".runtime", "steward", "health.jsonl")
}

func Record(townRoot string, snap Snapshot, bucket time.Duration) (bool, error) {
	if bucket <= 0 {
		bucket = DefaultBucket
	}
	if snap.Timestamp.IsZero() {
		snap.Timestamp = time.Now().UTC()
	}
	normalizeSnapshot(&snap)
	entry := Entry{Timestamp: snap.Timestamp.UTC(), BucketStart: snap.Timestamp.UTC().Truncate(bucket), Snapshot: snap}
	entry.Fingerprint = fingerprint(snap)
	entry.ID = entry.BucketStart.Format("20060102T150405Z") + "-" + entry.Fingerprint[:12]
	path := LedgerPath(townRoot)
	entries, err := Load(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, existing := range entries {
		if existing.BucketStart.Equal(entry.BucketStart) && existing.Fingerprint == entry.Fingerprint {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return false, err
	}
	_, err = f.Write(append(data, '\n'))
	return err == nil, err
}

func Load(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []Entry
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		entries = append(entries, e)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Timestamp.Before(entries[j].Timestamp) })
	return entries, nil
}

func ImportRuntime(townRoot string, bucket time.Duration) (int, int, error) {
	f, err := os.Open(RuntimePath(townRoot))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	return importRuntimeReader(townRoot, f, bucket)
}

func importRuntimeReader(townRoot string, r io.Reader, bucket time.Duration) (int, int, error) {
	imported, skipped := 0, 0
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		snap, err := snapshotFromRuntimeJSON([]byte(line))
		if err != nil {
			return imported, skipped, err
		}
		ok, err := Record(townRoot, snap, bucket)
		if err != nil {
			return imported, skipped, err
		}
		if ok {
			imported++
		} else {
			skipped++
		}
	}
	return imported, skipped, s.Err()
}

func BuildReport(townRoot string, since time.Time, recent int) (Report, error) {
	path := LedgerPath(townRoot)
	entries, err := Load(path)
	if err != nil {
		return Report{Path: path, Since: since, FindingsByKind: map[string]int{}, FindingsBySeverity: map[string]int{}}, err
	}
	rep := Report{Path: path, Since: since, FindingsByKind: map[string]int{}, FindingsBySeverity: map[string]int{}}
	var filtered []Entry
	for _, e := range entries {
		if !since.IsZero() && e.Timestamp.Before(since) {
			continue
		}
		filtered = append(filtered, e)
		for k, v := range e.Snapshot.FindingsByKind {
			rep.FindingsByKind[k] += v
		}
		for k, v := range e.Snapshot.FindingsBySeverity {
			rep.FindingsBySeverity[k] += v
		}
	}
	rep.Entries = len(filtered)
	if len(filtered) == 0 {
		return rep, nil
	}
	rep.FirstAt = filtered[0].Timestamp
	rep.LastAt = filtered[len(filtered)-1].Timestamp
	rep.RecoveryDebt = trend(filtered, func(e Entry) int { return e.Snapshot.RecoveryBlocked })
	rep.FindingsTotal = trend(filtered, func(e Entry) int { return e.Snapshot.FindingsTotal })
	if recent > 0 && recent < len(filtered) {
		rep.Recent = filtered[len(filtered)-recent:]
	} else if recent > 0 {
		rep.Recent = filtered
	}
	return rep, nil
}

func FormatReport(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Gas Town health history: %d entries since %s\n", rep.Entries, rep.Since.Format(time.RFC3339))
	if rep.Entries > 0 {
		fmt.Fprintf(&b, "Window: %s → %s\n", rep.FirstAt.Format(time.RFC3339), rep.LastAt.Format(time.RFC3339))
		fmt.Fprintf(&b, "Recovery debt: %d → %d (delta %+d, min %d, max %d)\n", rep.RecoveryDebt.First, rep.RecoveryDebt.Last, rep.RecoveryDebt.Delta, rep.RecoveryDebt.Min, rep.RecoveryDebt.Max)
		fmt.Fprintf(&b, "Findings total: %d → %d (delta %+d, min %d, max %d)\n", rep.FindingsTotal.First, rep.FindingsTotal.Last, rep.FindingsTotal.Delta, rep.FindingsTotal.Min, rep.FindingsTotal.Max)
	}
	writeCounts(&b, "Findings by severity", rep.FindingsBySeverity)
	writeCounts(&b, "Findings by kind", rep.FindingsByKind)
	return strings.TrimRight(b.String(), "\n")
}

func writeCounts(b *strings.Builder, title string, counts map[string]int) {
	fmt.Fprintf(b, "%s:\n", title)
	if len(counts) == 0 {
		fmt.Fprintln(b, "  none")
		return
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "  %s: %d\n", k, counts[k])
	}
}

func trend(entries []Entry, get func(Entry) int) Trend {
	first := get(entries[0])
	tr := Trend{First: first, Last: first, Min: first, Max: first}
	for _, e := range entries {
		v := get(e)
		tr.Last = v
		if v < tr.Min {
			tr.Min = v
		}
		if v > tr.Max {
			tr.Max = v
		}
	}
	tr.Delta = tr.Last - tr.First
	return tr
}

func snapshotFromRuntimeJSON(data []byte) (Snapshot, error) {
	var raw struct {
		Timestamp          time.Time      `json:"timestamp"`
		FindingsTotal      int            `json:"findings_total"`
		FindingsBySeverity map[string]int `json:"findings_by_severity"`
		FindingsByKind     map[string]int `json:"findings_by_kind"`
		Scheduler          struct {
			QueuedReady int `json:"queued_ready"`
			Capacity    struct {
				RecoveryBlocked int `json:"recovery_blocked"`
				ActiveSessions  int `json:"active_sessions"`
			} `json:"capacity"`
		} `json:"scheduler"`
		Polecats       map[string]int `json:"polecats"`
		DirtyCoreRepos []string       `json:"dirty_core_repos"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{Timestamp: raw.Timestamp, FindingsTotal: raw.FindingsTotal, FindingsBySeverity: raw.FindingsBySeverity, FindingsByKind: raw.FindingsByKind, RecoveryBlocked: raw.Scheduler.Capacity.RecoveryBlocked, QueuedReady: raw.Scheduler.QueuedReady, ActiveSessions: raw.Scheduler.Capacity.ActiveSessions, Polecats: raw.Polecats, DirtyCoreRepos: raw.DirtyCoreRepos}
	normalizeSnapshot(&s)
	return s, nil
}

func normalizeSnapshot(s *Snapshot) {
	if s.FindingsByKind == nil {
		s.FindingsByKind = map[string]int{}
	}
	if s.FindingsBySeverity == nil {
		s.FindingsBySeverity = map[string]int{}
	}
	if s.FindingsTotal == 0 && len(s.Findings) > 0 {
		s.FindingsTotal = len(s.Findings)
		for _, f := range s.Findings {
			s.FindingsByKind[f.Kind]++
			s.FindingsBySeverity[f.Severity]++
		}
	}
	if s.Timestamp.IsZero() {
		s.Timestamp = time.Now().UTC()
	}
	s.Timestamp = s.Timestamp.UTC()
	sort.Strings(s.DirtyCoreRepos)
}

func fingerprint(s Snapshot) string {
	normalizeSnapshot(&s)
	s.Timestamp = time.Time{}
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
