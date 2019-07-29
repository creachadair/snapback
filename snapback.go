// Copyright (C) 2018 Michael J. Fromberger. All Rights Reserved.

// Program snapback is a wrapper around the tarsnap command-line tool that
// makes it easier to create and manage backups of important directories.
//
// For more information about tarsnap, see http://www.tarsnap.com.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"text/template"
	"time"

	"bitbucket.org/creachadair/shell"
	"bitbucket.org/creachadair/stringset"
	"github.com/creachadair/snapback/config"
	"github.com/creachadair/staticfile"
	"github.com/creachadair/tarsnap"
)

const toolPackage = "github.com/creachadair/snapback"

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: %[1]s -find <path>... # find files in backups
       %[1]s -list           # list existing backups
       %[1]s -prune          # clean up old backups
       %[1]s -restore <dir>  # restore files or directories to <dir>
       %[1]s -size           # show sizes of stored data
       %[1]s -update         # update the tool from the network
       %[1]s [-v]            # create new backups of all sets
       %[1]s -c <name>...    # create new backups of specified sets

Create tarsnap backups of important directories. With the -v flag, the
underlying tarsnap commands will be logged to stderr. If -dry-run is true, no
archives are created or deleted.

With -find, the non-flag arguments specify file or directory paths to locate.
The output reports which backup sets contain each specified path. Paths that do
not match any known backup are omitted unless -v is also given.

With -list and -size, the non-flag arguments are used to select which archives
to list or evaluate. Globs are permitted in these arguments.

With -prune, archives filtered by expiration policies are deleted. Non-flag
arguments specify archive sets to evaluate for pruning. Archive ages are pruned
based on the current time. For testing, you may override this by setting -now.
Adding -dry-run to -prune also generates a log of the rule evaluations applied.

With -restore, the non-flag arguments specify files or directories to restore
into the specified output directory from the most recent matching backup.
The output directory is created if it does not exist. A path ending in "/"
identifies a directory, which is fully restored with all its contents.
Otherwise, it names a single file. To restore files from a different backup
(rather than the most recent), use -now.

Options:
`, filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
}

var (
	defaultConfig = "$HOME/.snapback"

	configFile = flag.String("config", defaultConfig, "Configuration file")
	outFormat  = flag.String("format", "", "Output format (Go template)")
	doCreate   = flag.Bool("c", false, "Create backups (default if no arguments are given)")
	doFind     = flag.Bool("find", false, "Find backups containing the specified paths")
	doList     = flag.Bool("list", false, "List known archives")
	doPrune    = flag.Bool("prune", false, "Prune out-of-band archives")
	doRestore  = flag.String("restore", "", "Restore files to this directory")
	doSize     = flag.Bool("size", false, "Print size statistics")
	doDryRun   = flag.Bool("dry-run", false, "Simulate creating or deleting archives")
	doUpdate   = flag.Bool("update", false, "Update the tool from the network")
	doVerbose  = flag.Bool("v", false, "Verbose logging")
	snapTime   = flag.String("now", "", "Effective current time (2006-01-02T15:04:05; default is wallclock time)")
)

func main() {
	flag.Parse()

	if *doUpdate {
		checkUpdate()
		return
	}
	dir, cfg, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Loading configuration: %v", err)
	}
	if cfg.Verbose {
		*doVerbose = true
	}
	if *doPrune && *doDryRun {
		cfg.Verbose = true
	}
	ts := &cfg.Config
	ts.CmdLog = logCommand
	if ts.WorkDir == "" {
		ts.WorkDir = dir
	}

	// Pre-check non-flag arguments to prune, to avoid a lookup in case the user
	// specified unknown backup sets.
	if *doPrune {
		s := stringset.New(flag.Args()...)
		v := stringset.FromIndexed(len(cfg.Backup), func(i int) string {
			return cfg.Backup[i].Name
		})
		if !s.IsSubset(v) {
			log.Fatalf("Unknown backup set names for -prune: %s", s.Diff(v))
		}
	}

	// If we need a list of existing archives, grab it.  For size calcluations
	// we only need the full archve listing if the user has given us globs to
	// select names from.
	var arch []tarsnap.Archive
	if *doList || *doPrune || (*doSize && hasGlob(flag.Args())) {
		arch, err = cfg.List()
		if err != nil {
			log.Fatalf("Listing archives: %v", err)
		}
	}
	if *doFind {
		findArchives(cfg, arch)
		return
	}
	if *doList {
		listArchives(cfg, arch)
		return
	}
	if *doPrune {
		pruneArchives(cfg, arch)
		return
	}
	if *doRestore != "" {
		restoreFiles(cfg, *doRestore)
		return
	}
	if *doSize {
		printSizes(cfg, arch)
		return
	} else if flag.NArg() != 0 && !*doCreate {
		log.Fatalf("Extra arguments after command: %v\n(use -c to back up specific sets)", flag.Args())
	}

	start := time.Now()
	if err := createBackups(cfg, flag.Args()); err != nil {
		log.Fatalf("Failed: %v", err)
	}
	elapsed := time.Since(start).Round(time.Second)
	cfg.List() // repair the list cache
	log.Printf("Backups finished [%v elapsed]", elapsed)
}

func findArchives(cfg *config.Config, _ []tarsnap.Archive) {
	if flag.NArg() == 0 {
		log.Fatal("No paths were specified to -find")
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 3, ' ', 0)
	for _, path := range flag.Args() {
		abs, err := filepath.Abs(path)
		if err != nil {
			log.Fatalf("Unable to resolve %q: %v", path, err)
		}
		bs := cfg.FindPath(abs)
		for _, b := range bs {
			fmt.Fprint(tw, b.Relative, "\t", b.Backup.Name, "\n")
		}
		if len(bs) == 0 && *doVerbose {
			fmt.Fprint(tw, path, "\t", "NONE", "\n")
		}
	}
	tw.Flush()
}

func listArchives(_ *config.Config, as []tarsnap.Archive) {
	ft := *outFormat
	if ft == "" {
		ft = "{{.Created}}\t{{.Name}}\n"
	} else if !strings.HasSuffix(ft, "\n") {
		ft += "\n"
	}
	t, err := template.New("list").Parse(ft)
	if err != nil {
		log.Fatalf("Parsing output format: %v", err)
	}
	for _, arch := range as {
		if matchExpr(arch.Name, flag.Args()) {
			err := t.Execute(os.Stdout, struct {
				Created string
				tarsnap.Archive
			}{Created: arch.Created.In(time.Local).Format(time.RFC3339), Archive: arch})
			if err != nil {
				log.Fatalf("Writing %v: %v", arch, err)
			}
		}
	}
}

func effectiveNow() time.Time {
	if *snapTime != "" {
		et, err := time.Parse("2006-01-02T15:04:05", *snapTime)
		if err != nil {
			log.Fatalf("Invalid time %q: %v", *snapTime, err)
		}
		return et
	}
	return time.Now()
}

func pruneArchives(cfg *config.Config, as []tarsnap.Archive) {
	start := time.Now()   // actual time, for operation latency
	now := effectiveNow() // effective time, for timestamp assignment
	chosen := as
	if flag.NArg() != 0 {
		chosen = nil
		s := stringset.New(flag.Args()...)
		for _, a := range as {
			if s.Contains(a.Base) {
				chosen = append(chosen, a)
			}
		}
	}

	var prune []string
	for _, p := range cfg.FindExpired(chosen, now) {
		prune = append(prune, p.Name)
	}
	if len(prune) == 0 {
		fmt.Fprintln(os.Stderr, "Nothing to prune")
		return
	} else if *doDryRun {
		fmt.Fprintln(os.Stderr, "-- Pruning would remove these archives:")
	} else if err := cfg.Config.Delete(prune...); err != nil {
		log.Fatalf("Deleting archives: %v", err)
	}
	elapsed := time.Since(start).Round(time.Second)
	cfg.List() // repair the list cache
	log.Printf("Pruning finished [%v elapsed]", elapsed)
	fmt.Println(strings.Join(prune, "\n"))
}

func restoreFiles(cfg *config.Config, dir string) {
	if flag.NArg() == 0 {
		log.Fatal("No paths were specified to -restore")
	}
	now := effectiveNow()

	// Locate the backup set for each requested path.  For now this must be
	// unique or it's an error.
	need := make(map[string][]string) // :: base → paths
	slow := make(map[string]bool)     // :: base → slow read necessary
	for _, path := range flag.Args() {
		abs, err := filepath.Abs(path)
		if err != nil {
			log.Fatalf("Unable to resolve %q: %v", path, err)
		}
		bs := cfg.FindPath(abs)
		if len(bs) == 0 {
			log.Fatalf("No backups found for %q", path)
		} else if len(bs) > 1 {
			log.Fatalf("Multiple backups found for %q", path)
		}
		n := bs[0].Backup.Name

		// If possible, we'll use fast reads to avoid having to scan the whole
		// archive.  But we can only do this if the user did not request the
		// restoration of directories.
		if strings.HasSuffix(path, "/") {
			slow[n] = true
		}
		need[n] = append(need[n], strings.TrimPrefix(bs[0].Relative, "/"))

		// N.B.: snapback creates archives without -P, so absolute paths are
		// trimmed by tarsnap when they are put into the archive. Removing the
		// leading slash here ensures the query path to tarsnap matches.
	}

	// Now that we have something to restore, it's worth listing the archives.
	fmt.Fprintln(os.Stderr, "-- Listing available archives")
	as, err := cfg.List()
	if err != nil {
		log.Fatalf("Listing archives: %v", err)
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatalf("Creating output directory: %v", err)
	}

	for set, paths := range need {
		opts := tarsnap.ExtractOptions{
			Include:            stringset.New(paths...).Elements(),
			WorkDir:            dir,
			RestorePermissions: true,
			FastRead:           !slow[set],
		}

		// Find the latest archive and run the extraction.
		arch, ok := tarsnap.Archives(as).LatestAsOf(set, now)
		if !ok {
			log.Fatalf("Unable to find the latest %q archive", set)
		}
		fmt.Fprintf(os.Stderr, "-- Restoring from %q\n » %s\n",
			arch.Name, strings.Join(opts.Include, "\n » "))
		if *doDryRun {
			fmt.Fprintln(os.Stderr, "[dry run, not restoring]")
		} else if err := cfg.Config.Extract(arch.Name, opts); err != nil {
			log.Fatalf("Extracting from %q: %v", arch.Name, err)
		}
	}
}

func printSizes(cfg *config.Config, as []tarsnap.Archive) {
	var names []string

	// If we have no archive list, it means the command-line arguments name
	// specific archives to size.
	if as == nil {
		names = flag.Args()
	} else {
		// Otherwise, we need to filter the archive list with flag globs.
		for _, a := range as {
			if matchExpr(a.Name, flag.Args()) {
				names = append(names, a.Name)
			}
		}
	}

	info, err := cfg.Config.Size(names...)
	if err != nil {
		log.Fatalf("Reading stats: %v", err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 8, 3, ' ', 0)
	fmt.Fprintf(tw, "TOTAL\t%s raw\t%s comp\t%s uniq\t%s incr\n",
		H(info.All.InputBytes), H(info.All.CompressedBytes),
		H(info.All.UniqueBytes), H(info.All.CompressedUniqueBytes))

	var subtotal tarsnap.Sizes
	var numPrinted int
	for _, name := range names {
		size, ok := info.Archive[name]
		if ok {
			subtotal.InputBytes += size.InputBytes
			subtotal.CompressedBytes += size.CompressedBytes
			subtotal.UniqueBytes += size.UniqueBytes
			subtotal.CompressedUniqueBytes += size.CompressedUniqueBytes

			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name,
				H(size.InputBytes), H(size.CompressedBytes),
				H(size.UniqueBytes), H(size.CompressedUniqueBytes))
			numPrinted++
		}
	}
	if numPrinted > 1 {
		fmt.Fprintf(tw, "SUBTOTAL\t%s\t%s\t%s\t%s\n",
			H(subtotal.InputBytes), H(subtotal.CompressedBytes),
			H(subtotal.UniqueBytes), H(subtotal.CompressedUniqueBytes))
	}
	tw.Flush()
}

func chooseBackups(cfg *config.Config, names []string) ([]*config.Backup, error) {
	if len(names) == 0 {
		return cfg.Backup, nil
	}
	seen := stringset.New()
	var sets []*config.Backup
	for _, name := range names {
		if seen.Contains(name) {
			return nil, fmt.Errorf("duplicate backup set %q", name)
		}

		set := cfg.FindSet(name)
		if set == nil {
			return nil, fmt.Errorf("no such backup set %q", name)
		}
		seen.Add(name)
		sets = append(sets, set)
	}
	return sets, nil
}

func createBackups(cfg *config.Config, names []string) error {
	sets, err := chooseBackups(cfg, names)
	if err != nil {
		return err
	}

	ts := time.Now()
	tag := "." + ts.Format("20060102-1504")
	nerrs := 0
	for _, b := range sets {
		opts := b.CreateOptions
		opts.DryRun = *doDryRun
		opts.CreationTime = ts
		name := b.Name + tag
		if err := cfg.Config.Create(name, opts); err != nil {
			log.Printf("ERROR: %s: %v", name, err)
			nerrs++
		} else {
			fmt.Println(name)
		}
	}
	if nerrs > 0 {
		return fmt.Errorf("%d errors", nerrs)
	}
	return nil
}

func checkUpdate() {
	fmt.Fprintf(os.Stderr, "-- Updating %s from the network\n", toolPackage)
	cmd := exec.Command("go", "get", "-u", toolPackage)
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	if _, err := cmd.Output(); err != nil {
		log.Fatalf("Updating %q failed: %v", toolPackage, err)
	}
	fmt.Fprintln(os.Stderr, "<done>")
}

func matchExpr(name string, exprs []string) bool {
	for _, expr := range exprs {
		ok, err := filepath.Match(expr, name)
		if ok && err == nil {
			return true
		}
	}
	return len(exprs) == 0
}

func loadConfig(path string) (string, *config.Config, error) {
	loc, err := filepath.Abs(os.ExpandEnv(path))
	if err != nil {
		return "", nil, err
	}
	f, err := staticfile.Open(loc)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	cfg, err := config.Parse(f)
	return filepath.Dir(loc), cfg, err
}

func logCommand(cmd string, args []string) {
	if *doVerbose {
		fmt.Fprintf(os.Stderr, "+ [%s] %s\n", cmd, shell.Join(args))
	}
}

// H converts z into a human-readable string.
func H(z int64) string {
	switch {
	case z < 1<<10:
		return strconv.FormatInt(z, 10)
	case z <= 1<<20:
		return fmt.Sprintf("%.1fK", float64(z)/(1<<10))
	case z <= 1<<30:
		return fmt.Sprintf("%.1fM", float64(z)/(1<<20))
	case z <= 1<<40:
		return fmt.Sprintf("%.1fG", float64(z)/(1<<30))
	default:
		return fmt.Sprintf("%.1fT", float64(z)/(1<<40))
	}
}

func isGlob(s string) bool {
	qt := false
	for _, c := range s {
		switch c {
		case '\\':
			qt = !qt
		case '[', '*', '?':
			if !qt {
				return true
			}
			qt = false
		}
	}
	return false
}

func hasGlob(args []string) bool {
	for _, arg := range args {
		if isGlob(arg) {
			return true
		}
	}
	return false
}
